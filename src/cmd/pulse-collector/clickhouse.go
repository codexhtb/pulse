package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type clickhouseWriter struct {
	conn clickhouse.Conn

	queue chan Event
	stop  chan struct{}
	done  chan struct{}

	batchSize     int
	flushInterval time.Duration

	closeOnce sync.Once

	queued         atomic.Uint64
	inserted       atomic.Uint64
	dropped        atomic.Uint64
	batches        atomic.Uint64
	failures       atomic.Uint64
	insertRateBits atomic.Uint64
	lastFailureNS  atomic.Int64
	lastDroppedNS  atomic.Int64
	onInserted     func([]Event)
}

type clickhouseWriterSnapshot struct {
	QueueDepth     int        `json:"queue_depth"`
	QueueCapacity  int        `json:"queue_capacity"`
	QueuedTotal    uint64     `json:"queued_total"`
	InsertedTotal  uint64     `json:"inserted_total"`
	InsertRate     float64    `json:"insert_rate_per_sec"`
	BatchCount     uint64     `json:"batch_count"`
	WriteFailures  uint64     `json:"write_failures"`
	DurableDropped uint64     `json:"durable_dropped"`
	LastFailureAt  *time.Time `json:"last_failure_at"`
	LastDroppedAt  *time.Time `json:"last_dropped_at"`
}

func newClickHouseWriter() (*clickhouseWriter, error) {
	host := getenv("CLICKHOUSE_HOST", "127.0.0.1")
	port := getenv("CLICKHOUSE_PORT", "9000")
	database := getenv("CLICKHOUSE_DATABASE", "pulse")
	user := getenv("CLICKHOUSE_USER", "pulse_ingest")
	password := os.Getenv("CLICKHOUSE_PASSWORD")

	if password == "" {
		return nil, fmt.Errorf("CLICKHOUSE_PASSWORD is empty")
	}

	batchSize := getenvInt("PULSE_BATCH_SIZE", 5000)
	queueSize := getenvInt("PULSE_QUEUE_SIZE", 100000)
	flushMS := getenvInt("PULSE_FLUSH_INTERVAL_MS", 500)

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{net.JoinHostPort(host, port)},
		Auth: clickhouse.Auth{
			Database: database,
			Username: user,
			Password: password,
		},
		DialTimeout: 5 * time.Second,
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}

	w := &clickhouseWriter{
		conn:          conn,
		queue:         make(chan Event, queueSize),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		batchSize:     batchSize,
		flushInterval: time.Duration(flushMS) * time.Millisecond,
	}

	go w.run()
	go w.logStats()

	log.Printf(
		"clickhouse writer ready addr=%s database=%s batch_size=%d queue_size=%d flush=%s",
		net.JoinHostPort(host, port),
		database,
		batchSize,
		queueSize,
		w.flushInterval,
	)

	return w, nil
}

func (w *clickhouseWriter) Enqueue(event Event) bool {
	select {
	case w.queue <- event:
		w.queued.Add(1)
		return true

	default:
		// Never block the dnstap ingest path indefinitely.
		// A persistent WAL/spool can be added later for zero-loss delivery.
		w.dropped.Add(1)
		w.lastDroppedNS.Store(time.Now().UTC().UnixNano())
		return false
	}
}

func (w *clickhouseWriter) Close() {
	w.closeOnce.Do(func() {
		close(w.stop)
		<-w.done
		_ = w.conn.Close()
	})
}

func (w *clickhouseWriter) run() {
	defer close(w.done)

	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	events := make([]Event, 0, w.batchSize)

	flush := func() {
		if len(events) == 0 {
			return
		}

		current := make([]Event, len(events))
		copy(current, events)
		events = events[:0]

		if err := w.writeWithRetry(current); err != nil {
			w.failures.Add(1)
			w.dropped.Add(uint64(len(current)))
			w.lastFailureNS.Store(time.Now().UTC().UnixNano())
			w.lastDroppedNS.Store(time.Now().UTC().UnixNano())

			log.Printf(
				"clickhouse batch permanently failed events=%d err=%v",
				len(current),
				err,
			)
		}
	}

	for {
		select {
		case event := <-w.queue:
			events = append(events, event)

			if len(events) >= w.batchSize {
				flush()
			}

		case <-ticker.C:
			flush()

		case <-w.stop:
			// Drain what is already queued before shutdown.
			for {
				select {
				case event := <-w.queue:
					events = append(events, event)

					if len(events) >= w.batchSize {
						flush()
					}

				default:
					flush()
					return
				}
			}
		}
	}
}

func (w *clickhouseWriter) writeWithRetry(events []Event) error {
	var lastErr error

	for attempt := 1; attempt <= 5; attempt++ {
		err := w.writeBatch(events)
		if err == nil {
			w.inserted.Add(uint64(len(events)))
			w.batches.Add(1)
			if w.onInserted != nil {
				w.onInserted(events)
			}
			return nil
		}

		lastErr = err

		delay := time.Duration(attempt*attempt) * 250 * time.Millisecond

		log.Printf(
			"clickhouse batch failed attempt=%d/5 events=%d retry_in=%s err=%v",
			attempt,
			len(events),
			delay,
			err,
		)

		time.Sleep(delay)
	}

	return lastErr
}

func (w *clickhouseWriter) writeBatch(events []Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	batch, err := w.conn.PrepareBatch(
		ctx,
		`INSERT INTO pulse.dns_events
		(
				event_time,
				ingested_at,
				query_time,
				response_time,
				outcome,
			tenant_id,
			source_id,
			client_ip,
			client_port,
			protocol,
			qname,
			qtype,
			qclass,
			rcode,
			dns_id,
			response_bytes,
			answer_count,
			latency_us,
			matched_query
		)`,
	)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	defer func() {
		if !batch.IsSent() {
			_ = batch.Abort()
		}
	}()

	for _, event := range events {
		ip := net.ParseIP(event.ClientIP)
		if ip == nil {
			return fmt.Errorf("invalid client IP %q", event.ClientIP)
		}

		var latency any

		if event.LatencyUS != nil {
			if *event.LatencyUS < 0 {
				latency = nil
			} else {
				latency = uint32(*event.LatencyUS)
			}
		} else {
			latency = nil
		}

		matched := uint8(0)
		if event.MatchedQuery {
			matched = 1
		}

		if err := batch.Append(
			event.EventTime,
			event.IngestedAt,
			event.QueryTime,
			event.ResponseTime,
			event.Outcome,
			event.TenantID,
			event.SourceID,
			ip,
			uint16(event.ClientPort),
			event.Protocol,
			event.QName,
			event.QType,
			event.QClass,
			event.RCode,
			event.DNSID,
			uint32(event.ResponseBytes),
			uint16(event.AnswerCount),
			latency,
			matched,
		); err != nil {
			return fmt.Errorf("append event: %w", err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send batch: %w", err)
	}

	return nil
}

func (w *clickhouseWriter) logStats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastInserted uint64

	for {
		select {
		case <-ticker.C:
			inserted := w.inserted.Load()
			delta := inserted - lastInserted
			lastInserted = inserted
			w.insertRateBits.Store(math.Float64bits(float64(delta) / 10))

			log.Printf(
				"storage stats queued=%d inserted=%d inserted_10s=%d batches=%d dropped=%d failures=%d queue_depth=%d/%d",
				w.queued.Load(),
				inserted,
				delta,
				w.batches.Load(),
				w.dropped.Load(),
				w.failures.Load(),
				len(w.queue),
				cap(w.queue),
			)

		case <-w.stop:
			return
		}
	}
}

func (w *clickhouseWriter) snapshot() clickhouseWriterSnapshot {
	return clickhouseWriterSnapshot{
		QueueDepth: len(w.queue), QueueCapacity: cap(w.queue),
		QueuedTotal: w.queued.Load(), InsertedTotal: w.inserted.Load(),
		InsertRate: math.Float64frombits(w.insertRateBits.Load()),
		BatchCount: w.batches.Load(), WriteFailures: w.failures.Load(),
		DurableDropped: w.dropped.Load(),
		LastFailureAt:  timeFromUnixNano(w.lastFailureNS.Load()),
		LastDroppedAt:  timeFromUnixNano(w.lastDroppedNS.Load()),
	}
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func getenvInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}

	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}

	return n
}
