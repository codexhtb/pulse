package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type livePublisher struct {
	conn *net.UDPConn
	dest *net.UDPAddr

	queue chan Event
	stop  chan struct{}
	done  chan struct{}

	closeOnce sync.Once

	queued       atomic.Uint64
	sent         atomic.Uint64
	dropped      atomic.Uint64
	errors       atomic.Uint64
	sentRateBits atomic.Uint64
	lastDropNS   atomic.Int64
	lastErrorNS  atomic.Int64
}

type livePublisherSnapshot struct {
	QueueDepth    int        `json:"queue_depth"`
	QueueCapacity int        `json:"queue_capacity"`
	QueuedTotal   uint64     `json:"queued_total"`
	SentTotal     uint64     `json:"sent_total"`
	SentRate      float64    `json:"sent_rate_per_sec"`
	Dropped       uint64     `json:"dropped"`
	UDPErrors     uint64     `json:"udp_errors"`
	LastDropAt    *time.Time `json:"last_drop_at"`
	LastErrorAt   *time.Time `json:"last_error_at"`
}

func newLivePublisher(addr string) (*livePublisher, error) {
	dest, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve live UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, fmt.Errorf("create live UDP socket: %w", err)
	}

	queueSize := getenvInt("PULSE_LIVE_QUEUE_SIZE", 50000)

	p := &livePublisher{
		conn:  conn,
		dest:  dest,
		queue: make(chan Event, queueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}

	go p.run()
	go p.logStats()

	log.Printf(
		"live publisher ready destination=%s queue_size=%d",
		addr,
		queueSize,
	)

	return p, nil
}

func (p *livePublisher) Enqueue(event Event) bool {
	select {
	case p.queue <- event:
		p.queued.Add(1)
		return true

	default:
		// Live is intentionally best-effort.
		// Never block persistent ClickHouse ingestion for UI traffic.
		p.dropped.Add(1)
		p.lastDropNS.Store(time.Now().UTC().UnixNano())
		return false
	}
}

func (p *livePublisher) Close() {
	p.closeOnce.Do(func() {
		close(p.stop)

		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			log.Printf("live publisher shutdown timeout")
		}

		_ = p.conn.Close()
	})
}

func (p *livePublisher) run() {
	defer close(p.done)

	send := func(event Event) {
		data, err := json.Marshal(event)
		if err != nil {
			p.errors.Add(1)
			p.lastErrorNS.Store(time.Now().UTC().UnixNano())
			return
		}

		if len(data) > 60000 {
			p.errors.Add(1)
			p.lastErrorNS.Store(time.Now().UTC().UnixNano())
			return
		}

		if _, err := p.conn.WriteToUDP(data, p.dest); err != nil {
			p.errors.Add(1)
			p.lastErrorNS.Store(time.Now().UTC().UnixNano())
			return
		}

		p.sent.Add(1)
	}

	for {
		select {
		case event := <-p.queue:
			send(event)

		case <-p.stop:
			for {
				select {
				case event := <-p.queue:
					send(event)

				default:
					return
				}
			}
		}
	}
}

func (p *livePublisher) logStats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastSent uint64

	for {
		select {
		case <-ticker.C:
			sent := p.sent.Load()
			delta := sent - lastSent
			lastSent = sent
			p.sentRateBits.Store(math.Float64bits(float64(delta) / 10))

			log.Printf(
				"live stats queued=%d sent=%d sent_10s=%d dropped=%d errors=%d queue_depth=%d/%d",
				p.queued.Load(),
				sent,
				delta,
				p.dropped.Load(),
				p.errors.Load(),
				len(p.queue),
				cap(p.queue),
			)

		case <-p.stop:
			return
		}
	}
}

func (p *livePublisher) snapshot() livePublisherSnapshot {
	return livePublisherSnapshot{
		QueueDepth: len(p.queue), QueueCapacity: cap(p.queue),
		QueuedTotal: p.queued.Load(), SentTotal: p.sent.Load(),
		SentRate: math.Float64frombits(p.sentRateBits.Load()),
		Dropped:  p.dropped.Load(), UDPErrors: p.errors.Load(),
		LastDropAt:  timeFromUnixNano(p.lastDropNS.Load()),
		LastErrorAt: timeFromUnixNano(p.lastErrorNS.Load()),
	}
}
