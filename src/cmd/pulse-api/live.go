package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	outcomeResponse          = "RESPONSE"
	outcomeNoResponse        = "NO_RESPONSE"
	outcomeUnmatchedResponse = "UNMATCHED_RESPONSE"
)

type liveEvent struct {
	EventTime     time.Time  `json:"event_time"`
	IngestedAt    time.Time  `json:"ingested_at"`
	QueryTime     *time.Time `json:"query_time"`
	ResponseTime  *time.Time `json:"response_time"`
	Outcome       string     `json:"outcome"`
	TenantID      string     `json:"tenant_id"`
	SourceID      string     `json:"source_id"`
	ClientIP      string     `json:"client_ip"`
	ClientPort    uint16     `json:"client_port"`
	Protocol      string     `json:"protocol"`
	QName         string     `json:"qname"`
	QType         string     `json:"qtype"`
	QClass        string     `json:"qclass"`
	RCode         string     `json:"rcode"`
	DNSID         uint16     `json:"dns_id"`
	ResponseBytes uint32     `json:"response_bytes"`
	AnswerCount   uint16     `json:"answer_count"`
	LatencyUS     *int64     `json:"latency_us"`
	MatchedQuery  bool       `json:"matched_query"`
}

type liveFilter struct {
	Tenant       string
	Source       string
	ClientIP     string
	Domain       string
	QType        string
	RCode        string
	Protocol     string
	Outcome      string
	FailuresOnly bool
	SlowUS       int64
}

type liveSubscriber struct {
	id     uint64
	filter liveFilter
	ch     chan liveEvent

	matched atomic.Uint64
	dropped atomic.Uint64
}

type liveHub struct {
	conn *net.UDPConn

	mu   sync.RWMutex
	subs map[uint64]*liveSubscriber

	nextID       atomic.Uint64
	received     atomic.Uint64
	decodeErrors atomic.Uint64
}

func newLiveHub(addr string) (*liveHub, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve live UDP listen address: %w", err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen live UDP: %w", err)
	}

	if err := conn.SetReadBuffer(4 * 1024 * 1024); err != nil {
		log.Printf("live UDP read buffer warning: %v", err)
	}

	h := &liveHub{
		conn: conn,
		subs: make(map[uint64]*liveSubscriber),
	}

	go h.readLoop()
	go h.logStats()

	log.Printf("live hub ready udp=%s", addr)

	return h, nil
}

func (h *liveHub) readLoop() {
	buffer := make([]byte, 65535)

	for {
		n, _, err := h.conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("live UDP read error: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var event liveEvent

		if err := json.Unmarshal(buffer[:n], &event); err != nil {
			h.decodeErrors.Add(1)
			continue
		}

		h.received.Add(1)

		h.mu.RLock()

		for _, sub := range h.subs {
			if !matchLiveFilter(event, sub.filter) {
				continue
			}

			sub.matched.Add(1)

			select {
			case sub.ch <- event:
			default:
				sub.dropped.Add(1)
			}
		}

		h.mu.RUnlock()
	}
}

func (h *liveHub) subscribe(filter liveFilter) *liveSubscriber {
	id := h.nextID.Add(1)

	sub := &liveSubscriber{
		id:     id,
		filter: filter,
		ch:     make(chan liveEvent, 2048),
	}

	h.mu.Lock()
	h.subs[id] = sub
	h.mu.Unlock()

	return sub
}

func (h *liveHub) unsubscribe(id uint64) {
	h.mu.Lock()
	delete(h.subs, id)
	h.mu.Unlock()
}

func (h *liveHub) subscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.subs)
}

func (h *liveHub) logStats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var lastReceived uint64

	for range ticker.C {
		received := h.received.Load()
		delta := received - lastReceived
		lastReceived = received

		log.Printf(
			"live hub stats received=%d received_10s=%d subscribers=%d decode_errors=%d",
			received,
			delta,
			h.subscriberCount(),
			h.decodeErrors.Load(),
		)
	}
}

func matchLiveFilter(event liveEvent, filter liveFilter) bool {
	if filter.Tenant != "" && event.TenantID != filter.Tenant {
		return false
	}

	if filter.Source != "" && event.SourceID != filter.Source {
		return false
	}

	if filter.ClientIP != "" {
		eventIP := net.ParseIP(event.ClientIP)
		filterIP := net.ParseIP(filter.ClientIP)

		if eventIP == nil || filterIP == nil || !eventIP.Equal(filterIP) {
			return false
		}
	}

	if filter.Domain != "" &&
		normalizeDomain(event.QName) != filter.Domain {
		return false
	}

	if filter.QType != "" &&
		strings.ToUpper(event.QType) != filter.QType {
		return false
	}

	if filter.RCode != "" &&
		strings.ToUpper(event.RCode) != filter.RCode {
		return false
	}

	if filter.Protocol != "" &&
		strings.ToUpper(event.Protocol) != filter.Protocol {
		return false
	}

	if filter.Outcome != "" && strings.ToUpper(event.Outcome) != filter.Outcome {
		return false
	}
	if filter.FailuresOnly && !failureEvent(event.Outcome, event.RCode) {
		return false
	}
	if filter.SlowUS > 0 && (event.Outcome != outcomeResponse || event.LatencyUS == nil || *event.LatencyUS < filter.SlowUS) {
		return false
	}

	return true
}

func parseLiveFilter(r *http.Request, tenant string) (liveFilter, error) {
	filter := liveFilter{
		Tenant:       tenant,
		Source:       strings.TrimSpace(r.URL.Query().Get("source")),
		ClientIP:     strings.TrimSpace(r.URL.Query().Get("client_ip")),
		Domain:       normalizeDomain(r.URL.Query().Get("domain")),
		QType:        strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("qtype"))),
		RCode:        strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("rcode"))),
		Protocol:     strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("protocol"))),
		Outcome:      strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("outcome"))),
		FailuresOnly: parseBoolQuery(r.URL.Query().Get("failures")),
	}
	if value := strings.TrimSpace(r.URL.Query().Get("slow_us")); value != "" {
		slow, err := strconv.ParseInt(value, 10, 64)
		if err != nil || slow < 0 {
			return filter, fmt.Errorf("slow_us must be a non-negative integer")
		}
		filter.SlowUS = slow
	}

	if filter.ClientIP != "" && net.ParseIP(filter.ClientIP) == nil {
		return filter, fmt.Errorf("invalid client_ip")
	}

	if filter.Protocol != "" &&
		filter.Protocol != "UDP" &&
		filter.Protocol != "TCP" {
		return filter, fmt.Errorf("protocol must be UDP or TCP")
	}
	if filter.Outcome != "" && filter.Outcome != outcomeResponse && filter.Outcome != outcomeNoResponse && filter.Outcome != outcomeUnmatchedResponse {
		return filter, fmt.Errorf("invalid outcome")
	}

	return filter, nil
}

func failureEvent(outcome, rcode string) bool {
	if strings.EqualFold(outcome, outcomeNoResponse) {
		return true
	}
	switch strings.ToUpper(rcode) {
	case "SERVFAIL", "REFUSED", "FORMERR":
		return true
	default:
		return false
	}
}

func parseBoolQuery(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "true" || value == "yes"
}

func parseLiveRate(r *http.Request) int {
	value := strings.TrimSpace(r.URL.Query().Get("rate"))

	if value == "" {
		return 200
	}

	rate, err := strconv.Atoi(value)
	if err != nil {
		return 200
	}

	if rate < 1 {
		return 1
	}

	if rate > 1000 {
		return 1000
	}

	return rate
}

func (s *server) liveStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(
			w,
			http.StatusInternalServerError,
			fmt.Errorf("streaming unsupported"),
		)
		return
	}

	filter, err := parseLiveFilter(r, s.tenant)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	rateLimit := parseLiveRate(r)

	sub := s.live.subscribe(filter)
	defer s.live.unsubscribe(sub.id)

	// pulse-api normally has a finite WriteTimeout.
	// SSE is intentionally long-lived.
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ready := map[string]any{
		"status":     "connected",
		"rate_limit": rateLimit,
		"filter": map[string]any{
			"source":    filter.Source,
			"client_ip": filter.ClientIP,
			"domain":    filter.Domain,
			"qtype":     filter.QType,
			"rcode":     filter.RCode,
			"protocol":  filter.Protocol,
			"outcome":   filter.Outcome,
			"failures":  filter.FailuresOnly,
			"slow_us":   filter.SlowUS,
		},
		"time": time.Now().UTC(),
	}

	readyJSON, _ := json.Marshal(ready)

	if _, err := fmt.Fprintf(
		w,
		"event: ready\ndata: %s\n\n",
		readyJSON,
	); err != nil {
		return
	}

	flusher.Flush()

	statsTicker := time.NewTicker(time.Second)
	defer statsTicker.Stop()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	windowStart := time.Now()
	sentWindow := 0
	suppressedWindow := uint64(0)

	lastMatched := sub.matched.Load()
	lastQueueDropped := sub.dropped.Load()

	for {
		select {
		case <-r.Context().Done():
			return

		case event := <-sub.ch:
			if time.Since(windowStart) >= time.Second {
				windowStart = time.Now()
				sentWindow = 0
			}

			if sentWindow >= rateLimit {
				suppressedWindow++
				continue
			}

			data, err := json.Marshal(event)
			if err != nil {
				continue
			}

			if _, err := fmt.Fprintf(
				w,
				"event: dns\ndata: %s\n\n",
				data,
			); err != nil {
				return
			}

			flusher.Flush()
			sentWindow++

		case <-statsTicker.C:
			matched := sub.matched.Load()
			queueDropped := sub.dropped.Load()

			stats := map[string]any{
				"matched_eps":     matched - lastMatched,
				"shown_eps":       sentWindow,
				"suppressed":      suppressedWindow,
				"queue_dropped":   queueDropped - lastQueueDropped,
				"rate_limit":      rateLimit,
				"subscribers":     s.live.subscriberCount(),
				"server_received": s.live.received.Load(),
			}

			lastMatched = matched
			lastQueueDropped = queueDropped

			statsJSON, _ := json.Marshal(stats)

			if _, err := fmt.Fprintf(
				w,
				"event: stats\ndata: %s\n\n",
				statsJSON,
			); err != nil {
				return
			}

			flusher.Flush()

			windowStart = time.Now()
			sentWindow = 0
			suppressedWindow = 0

		case <-heartbeatTicker.C:
			if _, err := fmt.Fprintf(
				w,
				": heartbeat %d\n\n",
				time.Now().Unix(),
			); err != nil {
				return
			}

			flusher.Flush()
		}
	}
}
