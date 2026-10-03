package main

import (
	"net"
	"testing"
	"time"

	dnstap "github.com/dnstap/golang-dnstap"
	"github.com/miekg/dns"
)

func pointer[T any](value T) *T { return &value }

func testCollector(timeout time.Duration) (*collector, *[]Event) {
	events := make([]Event, 0)
	c := &collector{
		tenant: "default", startedAt: time.Now().UTC(), correlationTimeout: timeout,
		pending: make(map[string]pendingQuery), recent: make(map[string]recentTerminal),
		sources: make(map[string]*sourceRuntime),
	}
	c.emitHook = func(event Event) { events = append(events, event) }
	return c, &events
}

func testMessage(t *testing.T, response bool, rcode int, id uint16, at time.Time) *dnstap.Message {
	t.Helper()
	packet := new(dns.Msg)
	packet.Id = id
	packet.Question = []dns.Question{{Name: "example.test.", Qtype: dns.TypeA, Qclass: dns.ClassINET}}
	if response {
		packet.Response = true
		packet.Rcode = rcode
	}
	wire, err := packet.Pack()
	if err != nil {
		t.Fatal(err)
	}
	protocol := dnstap.SocketProtocol_UDP
	port := uint32(53000)
	sec, nsec := uint64(at.Unix()), uint32(at.Nanosecond())
	message := &dnstap.Message{SocketProtocol: &protocol, QueryAddress: net.ParseIP("192.0.2.10").To4(), QueryPort: &port}
	if response {
		message.ResponseMessage = wire
		message.ResponseTimeSec = &sec
		message.ResponseTimeNsec = &nsec
	} else {
		message.QueryMessage = wire
		message.QueryTimeSec = &sec
		message.QueryTimeNsec = &nsec
	}
	return message
}

func TestResponseOutcomesAndRCodes(t *testing.T) {
	for _, test := range []struct {
		name  string
		rcode int
		want  string
	}{{"normal", dns.RcodeSuccess, "NOERROR"}, {"nxdomain", dns.RcodeNameError, "NXDOMAIN"}, {"servfail", dns.RcodeServerFailure, "SERVFAIL"}} {
		t.Run(test.name, func(t *testing.T) {
			c, events := testCollector(time.Second)
			queryAt := time.Now().UTC().Add(-10 * time.Millisecond)
			c.handleClientQuery("dns1", testMessage(t, false, 0, 10, queryAt))
			c.handleClientResponse("dns1", testMessage(t, true, test.rcode, 10, queryAt.Add(5*time.Millisecond)))
			if len(*events) != 1 {
				t.Fatalf("events=%d", len(*events))
			}
			e := (*events)[0]
			if e.Outcome != outcomeResponse || e.RCode != test.want || e.QueryTime == nil || e.ResponseTime == nil || e.LatencyUS == nil {
				t.Fatalf("unexpected event: %+v", e)
			}
			if !e.EventTime.Equal(queryAt) {
				t.Fatalf("event_time=%s want query_time=%s", e.EventTime, queryAt)
			}
		})
	}
}

func TestNoResponseUsesOriginalQueryTime(t *testing.T) {
	c, events := testCollector(20 * time.Millisecond)
	queryAt := time.Now().UTC().Add(-time.Second)
	c.handleClientQuery("dns1", testMessage(t, false, 0, 11, queryAt))
	c.stateMu.Lock()
	expires := c.expiryQueue[0].ExpiresAt
	c.stateMu.Unlock()
	c.expirePending(expires.Add(time.Millisecond))
	if len(*events) != 1 {
		t.Fatalf("events=%d", len(*events))
	}
	e := (*events)[0]
	if e.Outcome != outcomeNoResponse || e.QueryTime == nil || e.ResponseTime != nil || e.RCode != "" || e.LatencyUS != nil || e.ResponseBytes != 0 || e.AnswerCount != 0 {
		t.Fatalf("unexpected event: %+v", e)
	}
	if !e.EventTime.Equal(queryAt) {
		t.Fatalf("NO_RESPONSE event_time moved to expiration: %s", e.EventTime)
	}
}

func TestUnmatchedResponse(t *testing.T) {
	c, events := testCollector(time.Second)
	c.handleClientResponse("dns1", testMessage(t, true, dns.RcodeRefused, 12, time.Now().UTC()))
	if len(*events) != 1 || (*events)[0].Outcome != outcomeUnmatchedResponse || (*events)[0].QueryTime != nil || (*events)[0].ResponseTime == nil || (*events)[0].LatencyUS != nil || (*events)[0].RCode != "REFUSED" {
		t.Fatalf("unexpected events: %+v", *events)
	}
}

func TestDuplicateMatchedResponseIsSuppressed(t *testing.T) {
	c, events := testCollector(time.Second)
	queryAt := time.Now().UTC().Add(-time.Millisecond)
	query := testMessage(t, false, 0, 15, queryAt)
	response := testMessage(t, true, dns.RcodeSuccess, 15, time.Now().UTC())
	c.handleClientQuery("dns1", query)
	c.handleClientResponse("dns1", response)
	c.handleClientResponse("dns1", response)
	if len(*events) != 1 || c.metrics.duplicatesSuppressed.Load() != 1 {
		t.Fatalf("events=%d duplicates=%d", len(*events), c.metrics.duplicatesSuppressed.Load())
	}
}

func TestLateResponseThenDuplicateIsSuppressed(t *testing.T) {
	c, events := testCollector(20 * time.Millisecond)
	queryAt := time.Now().UTC().Add(-time.Second)
	query := testMessage(t, false, 0, 13, queryAt)
	response := testMessage(t, true, dns.RcodeSuccess, 13, time.Now().UTC())
	c.handleClientQuery("dns1", query)
	c.stateMu.Lock()
	expires := c.expiryQueue[0].ExpiresAt
	c.stateMu.Unlock()
	c.expirePending(expires.Add(time.Millisecond))
	c.handleClientResponse("dns1", response)
	c.handleClientResponse("dns1", response)
	if len(*events) != 2 || (*events)[0].Outcome != outcomeNoResponse || (*events)[1].Outcome != outcomeUnmatchedResponse {
		t.Fatalf("unexpected events: %+v", *events)
	}
	if c.metrics.duplicatesSuppressed.Load() != 1 {
		t.Fatalf("duplicates=%d", c.metrics.duplicatesSuppressed.Load())
	}
}

func TestStaleExpiryDoesNotDeleteReusedKey(t *testing.T) {
	c, events := testCollector(50 * time.Millisecond)
	at := time.Now().UTC()
	c.handleClientQuery("dns1", testMessage(t, false, 0, 14, at))
	c.stateMu.Lock()
	first := c.expiryQueue[0]
	c.stateMu.Unlock()
	time.Sleep(2 * time.Millisecond)
	c.handleClientQuery("dns1", testMessage(t, false, 0, 14, at.Add(time.Millisecond)))
	c.expirePending(first.ExpiresAt.Add(time.Microsecond))
	c.stateMu.Lock()
	count := len(c.pending)
	secondExpires := c.expiryQueue[1].ExpiresAt
	c.stateMu.Unlock()
	if count != 1 || len(*events) != 0 {
		t.Fatalf("stale marker removed current pending: pending=%d events=%d", count, len(*events))
	}
	c.expirePending(secondExpires.Add(time.Microsecond))
	if len(*events) != 1 {
		t.Fatalf("new pending did not expire: events=%d", len(*events))
	}
}

func TestClickHouseQueueSaturationCountsDurableDrop(t *testing.T) {
	w := &clickhouseWriter{queue: make(chan Event, 1)}
	if !w.Enqueue(Event{}) {
		t.Fatal("first enqueue failed")
	}
	if w.Enqueue(Event{}) {
		t.Fatal("second enqueue unexpectedly succeeded")
	}
	s := w.snapshot()
	if s.DurableDropped != 1 || s.QueueDepth != 1 || s.QueueCapacity != 1 {
		t.Fatalf("snapshot=%+v", s)
	}
}

func TestRuntimeCountersAreSinceProcessStart(t *testing.T) {
	c, _ := testCollector(time.Second)
	now := c.startedAt.Add(3 * time.Second)
	s := c.statusSnapshot(now)
	if s.UptimeSeconds != 3 || !s.ProcessStartedAt.Equal(c.startedAt) {
		t.Fatalf("status=%+v", s)
	}
	for name, value := range s.CountersSinceStart {
		if value != 0 {
			t.Fatalf("counter %s=%d, want reset", name, value)
		}
	}
}

func TestExpectedSourceMissingIsReported(t *testing.T) {
	c, _ := testCollector(time.Second)
	c.expectedSources = map[string]struct{}{"dns1": {}, "dns2": {}}
	c.sources["192.0.2.53"] = &sourceRuntime{
		Identity:          "dns1",
		ActiveConnections: 1,
		Expected:          true,
		SeenSinceStart:    true,
		LastMessageAt:     time.Now().UTC(),
	}

	status := c.statusSnapshot(time.Now().UTC())
	if len(status.Sources) != 2 {
		t.Fatalf("sources=%+v", status.Sources)
	}
	if status.Sources[0].Identity != "dns1" || !status.Sources[0].Expected || !status.Sources[0].SeenSinceStart {
		t.Fatalf("active expected source=%+v", status.Sources[0])
	}
	if status.Sources[1].Identity != "dns2" || !status.Sources[1].Expected || status.Sources[1].SeenSinceStart || status.Sources[1].ActiveConnections != 0 {
		t.Fatalf("missing expected source=%+v", status.Sources[1])
	}
}
