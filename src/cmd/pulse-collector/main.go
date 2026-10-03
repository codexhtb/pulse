package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	dnstap "github.com/dnstap/golang-dnstap"
	"github.com/miekg/dns"
)

type Event struct {
	EventTime    time.Time  `json:"event_time"`
	IngestedAt   time.Time  `json:"ingested_at"`
	QueryTime    *time.Time `json:"query_time"`
	ResponseTime *time.Time `json:"response_time"`
	Outcome      string     `json:"outcome"`

	TenantID string `json:"tenant_id"`
	SourceID string `json:"source_id"`

	ClientIP   string `json:"client_ip"`
	ClientPort uint32 `json:"client_port"`

	Protocol string `json:"protocol"`

	QName  string `json:"qname"`
	QType  string `json:"qtype"`
	QClass string `json:"qclass"`

	RCode string `json:"rcode"`

	DNSID uint16 `json:"dns_id"`

	ResponseBytes int `json:"response_bytes"`
	AnswerCount   int `json:"answer_count"`

	LatencyUS    *int64 `json:"latency_us"`
	MatchedQuery bool   `json:"matched_query"`
}

type pendingQuery struct {
	ID        uint64
	ExpiresAt time.Time
	Event     Event
}

type expiryEntry struct {
	Key       string
	ID        uint64
	ExpiresAt time.Time
}

type recentTerminal struct {
	Outcome string
	At      time.Time
}

const (
	outcomeResponse          = "RESPONSE"
	outcomeNoResponse        = "NO_RESPONSE"
	outcomeUnmatchedResponse = "UNMATCHED_RESPONSE"
	recentTerminalTTL        = 5 * time.Second
)

type collectorMetrics struct {
	activeConnections    atomic.Int64
	framesReceived       atomic.Uint64
	clientQueries        atomic.Uint64
	clientResponses      atomic.Uint64
	transactionsEmitted  atomic.Uint64
	matchedResponses     atomic.Uint64
	noResponses          atomic.Uint64
	unmatchedResponses   atomic.Uint64
	duplicatesSuppressed atomic.Uint64
	pendingPeak          atomic.Uint64
	lastDNSTapEventNS    atomic.Int64
	normalizedRateBits   atomic.Uint64
}

type sourceRuntime struct {
	Identity          string    `json:"identity"`
	RemoteAddress     string    `json:"remote_address"`
	ActiveConnections int       `json:"active_connections"`
	ConnectedAt       time.Time `json:"connected_at,omitempty"`
	LastMessageAt     time.Time `json:"last_message_at,omitempty"`
	LastPersistedAt   time.Time `json:"last_persisted_at,omitempty"`
	Expected          bool      `json:"expected"`
	SeenSinceStart    bool      `json:"seen_since_start"`
	Frames            uint64    `json:"frames_received"`
	Queries           uint64    `json:"queries_received"`
	Responses         uint64    `json:"responses_received"`
	Transactions      uint64    `json:"transactions_emitted"`
	NoResponse        uint64    `json:"no_response"`
	Unmatched         uint64    `json:"unmatched_responses"`
	Errors            uint64    `json:"errors"`
	Rate              float64   `json:"transactions_per_sec"`
	lastRateTotal     uint64
}

type collector struct {
	tenant             string
	startedAt          time.Time
	correlationTimeout time.Duration

	stateMu       sync.Mutex
	checkpointMu  sync.Mutex
	pending       map[string]pendingQuery
	expiryQueue   []expiryEntry
	expiryHead    int
	nextPendingID uint64
	recent        map[string]recentTerminal
	metrics       collectorMetrics
	pendingStore  *pendingStore

	handlerMu      sync.Mutex
	handlerWG      sync.WaitGroup
	activeHandlers map[net.Conn]struct{}
	shuttingDown   bool
	backgroundStop chan struct{}
	backgroundWG   sync.WaitGroup
	backgroundOnce sync.Once

	sourceMu        sync.RWMutex
	sources         map[string]*sourceRuntime
	expectedSources map[string]struct{}

	writer      *clickhouseWriter
	live        *livePublisher
	debugEvents bool

	outputMu sync.Mutex
	encoder  *json.Encoder
	emitHook func(Event)
}

type parsedDNS struct {
	ID          uint16
	QName       string
	QType       uint16
	QClass      uint16
	RCode       int
	AnswerCount int
}

func main() {
	listenAddr := flag.String(
		"listen",
		"127.0.0.1:6000",
		"DNSTAP TCP listen address",
	)

	tenant := flag.String(
		"tenant",
		"default",
		"tenant identifier",
	)

	clickhouseEnabled := flag.Bool(
		"clickhouse",
		true,
		"enable ClickHouse storage",
	)

	debugEvents := flag.Bool(
		"debug-events",
		false,
		"write every normalized DNS event as JSON to stdout",
	)

	flag.Parse()

	correlationTimeout := time.Duration(getenvInt("PULSE_CORRELATION_TIMEOUT_MS", 30000)) * time.Millisecond
	expectedSources := parseCSVSet(os.Getenv("PULSE_EXPECTED_SOURCES"))

	c := &collector{
		tenant:             *tenant,
		startedAt:          time.Now().UTC(),
		correlationTimeout: correlationTimeout,
		pending:            make(map[string]pendingQuery),
		recent:             make(map[string]recentTerminal),
		sources:            make(map[string]*sourceRuntime),
		expectedSources:    expectedSources,
		activeHandlers:     make(map[net.Conn]struct{}),
		backgroundStop:     make(chan struct{}),
		debugEvents:        *debugEvents,
		encoder:            json.NewEncoder(os.Stdout),
	}

	pendingDir := strings.TrimSpace(os.Getenv("PULSE_PENDING_STATE_DIR"))
	store, recovery, err := openPendingStore(
		pendingDir,
		time.Duration(getenvInt("PULSE_PENDING_FLUSH_MS", 10))*time.Millisecond,
		time.Duration(getenvInt("PULSE_PENDING_SYNC_MS", 100))*time.Millisecond,
		time.Duration(getenvInt("PULSE_PENDING_CHECKPOINT_MS", 60000))*time.Millisecond,
	)
	if err != nil {
		log.Fatalf("initialize pending persistence: %v", err)
	}
	c.pendingStore = store
	c.pending = recovery.Pending
	c.recent = recovery.Recent
	c.nextPendingID = recovery.NextPendingID
	for key, pending := range c.pending {
		c.expiryQueue = append(c.expiryQueue, expiryEntry{Key: key, ID: pending.ID, ExpiresAt: pending.ExpiresAt})
	}
	sort.Slice(c.expiryQueue, func(i, j int) bool { return c.expiryQueue[i].ExpiresAt.Before(c.expiryQueue[j].ExpiresAt) })
	updatePeak(&c.metrics.pendingPeak, uint64(len(c.pending)))

	if *clickhouseEnabled {
		writer, err := newClickHouseWriter()
		if err != nil {
			log.Fatalf("initialize ClickHouse writer: %v", err)
		}

		c.writer = writer
		writer.onInserted = c.recordPersisted
	}

	if liveAddr := os.Getenv("PULSE_LIVE_UDP"); liveAddr != "" {
		live, err := newLivePublisher(liveAddr)
		if err != nil {
			log.Printf("live publisher disabled: %v", err)
		} else {
			c.live = live
		}
	}

	if recovered := c.expirePending(time.Now().UTC()); recovered > 0 {
		log.Printf("expired recovered pending transactions=%d", recovered)
	}

	c.startBackgroundWorkers()

	statusListen := getenv("PULSE_COLLECTOR_STATUS_LISTEN", "127.0.0.1:9093")
	statusServer := c.startStatusServer(statusListen)

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", *listenAddr, err)
	}

	log.Printf(
		"pulse-collector started listen=%s tenant=%s correlation_timeout=%s status=%s pending_persistence=%t expected_sources=%d recovered_pending=%d",
		*listenAddr,
		*tenant,
		correlationTimeout,
		statusListen,
		c.pendingStore != nil,
		len(expectedSources),
		recovery.Recovered,
	)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signals
		log.Printf("shutdown requested")
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}

			log.Printf("accept error: %v", err)
			continue
		}

		log.Printf(
			"dnstap connection remote=%s",
			conn.RemoteAddr(),
		)

		c.startConnectionHandler(conn)
	}

	c.gracefulShutdown(statusServer)
}

func (c *collector) startConnectionHandler(conn net.Conn) {
	c.handlerMu.Lock()
	if c.shuttingDown {
		c.handlerMu.Unlock()
		_ = conn.Close()
		return
	}
	if c.activeHandlers == nil {
		c.activeHandlers = make(map[net.Conn]struct{})
	}
	c.activeHandlers[conn] = struct{}{}
	c.handlerWG.Add(1)
	c.handlerMu.Unlock()

	go func() {
		defer c.handlerWG.Done()
		defer func() {
			c.handlerMu.Lock()
			delete(c.activeHandlers, conn)
			c.handlerMu.Unlock()
		}()
		c.handleConnection(conn)
	}()
}

func (c *collector) stopIntakeHandlers() {
	c.handlerMu.Lock()
	c.shuttingDown = true
	connections := make([]net.Conn, 0, len(c.activeHandlers))
	for conn := range c.activeHandlers {
		connections = append(connections, conn)
	}
	c.handlerMu.Unlock()

	for _, conn := range connections {
		_ = conn.Close()
	}
	c.handlerWG.Wait()
}

func (c *collector) startBackgroundWorkers() {
	if c.backgroundStop == nil {
		c.backgroundStop = make(chan struct{})
	}
	c.backgroundWG.Add(1)
	go func() {
		defer c.backgroundWG.Done()
		c.pendingJanitor()
	}()
	if c.pendingStore != nil {
		c.backgroundWG.Add(1)
		go func() {
			defer c.backgroundWG.Done()
			c.pendingCheckpointLoop()
		}()
	}
	c.backgroundWG.Add(1)
	go func() {
		defer c.backgroundWG.Done()
		c.sampleRates()
	}()
}

func (c *collector) stopBackgroundWorkers() {
	c.backgroundOnce.Do(func() {
		if c.backgroundStop != nil {
			close(c.backgroundStop)
		}
	})
	c.backgroundWG.Wait()
}

func (c *collector) gracefulShutdown(statusServer *http.Server) {
	log.Printf("shutdown phase=dnstap_handlers")
	c.stopIntakeHandlers()
	log.Printf("shutdown phase=background_workers")
	c.stopBackgroundWorkers()
	if c.writer != nil {
		log.Printf("shutdown phase=persistence_drain queue_depth=%d", len(c.writer.queue))
		c.writer.Close()
	}
	if c.live != nil {
		log.Printf("shutdown phase=live_drain queue_depth=%d", len(c.live.queue))
		c.live.Close()
	}
	log.Printf("shutdown phase=pending_checkpoint")
	c.closePendingStore()
	if statusServer != nil {
		_ = statusServer.Close()
	}
	log.Printf("shutdown complete")
}

func (c *collector) handleConnection(conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	remoteID := remoteHost(remote)

	reader, err := dnstap.NewReader(
		conn,
		&dnstap.ReaderOptions{
			Bidirectional: true,
			Timeout:       5 * time.Second,
		},
	)
	if err != nil {
		log.Printf(
			"dnstap handshake failed remote=%s err=%v",
			remote,
			err,
		)
		return
	}

	c.metrics.activeConnections.Add(1)
	c.connectionStarted(remoteID, remote)
	defer func() {
		c.metrics.activeConnections.Add(-1)
		c.connectionEnded(remoteID)
	}()

	decoder := dnstap.NewDecoder(reader, 256*1024)

	for {
		var tap dnstap.Dnstap

		err := decoder.Decode(&tap)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf(
					"dnstap disconnected remote=%s err=%v",
					remote,
					err,
				)
			}
			return
		}

		c.metrics.framesReceived.Add(1)

		msg := tap.GetMessage()
		if msg == nil {
			continue
		}

		sourceID := strings.TrimSpace(string(tap.GetIdentity()))
		if sourceID == "" {
			sourceID = remoteID
		}

		c.sourceMessage(remoteID, sourceID, remote)
		c.metrics.lastDNSTapEventNS.Store(time.Now().UTC().UnixNano())

		switch msg.GetType() {
		case dnstap.Message_CLIENT_QUERY:
			c.handleClientQuery(sourceID, msg)

		case dnstap.Message_CLIENT_RESPONSE:
			c.handleClientResponse(sourceID, msg)
		}
	}
}

func (c *collector) handleClientQuery(
	sourceID string,
	msg *dnstap.Message,
) {
	c.metrics.clientQueries.Add(1)

	wire := msg.GetQueryMessage()
	if len(wire) == 0 {
		c.sourceError(sourceID)
		return
	}

	parsed, err := parseDNS(wire)
	if err != nil {
		c.sourceError(sourceID)
		log.Printf("unable to parse CLIENT_QUERY source=%s err=%v", sourceID, err)
		return
	}

	clientIP := net.IP(msg.GetQueryAddress()).String()
	if clientIP == "<nil>" {
		clientIP = ""
	}

	protocol := msg.GetSocketProtocol().String()

	key := makeKey(
		sourceID,
		clientIP,
		msg.GetQueryPort(),
		protocol,
		parsed,
	)

	queryTime := timestamp(
		msg.GetQueryTimeSec(),
		msg.GetQueryTimeNsec(),
	)

	now := time.Now().UTC()

	if queryTime.IsZero() {
		queryTime = now
	}

	queryTimeCopy := queryTime

	c.stateMu.Lock()
	c.nextPendingID++
	id := c.nextPendingID
	pending := pendingQuery{
		ID:        id,
		ExpiresAt: now.Add(c.correlationTimeout),
		Event: Event{
			EventTime:    queryTime,
			IngestedAt:   now,
			QueryTime:    &queryTimeCopy,
			Outcome:      outcomeNoResponse,
			TenantID:     c.tenant,
			SourceID:     sourceID,
			ClientIP:     clientIP,
			ClientPort:   msg.GetQueryPort(),
			Protocol:     protocol,
			QName:        parsed.QName,
			QType:        qtypeName(parsed.QType),
			QClass:       qclassName(parsed.QClass),
			DNSID:        parsed.ID,
			MatchedQuery: true,
		},
	}
	c.pending[key] = pending
	c.expiryQueue = append(c.expiryQueue, expiryEntry{Key: key, ID: id, ExpiresAt: pending.ExpiresAt})
	delete(c.recent, key)
	if err := c.pendingStore.appendPut(key, pending); err != nil {
		log.Printf("persist pending query source=%s client=%s qname=%s err=%v", sourceID, clientIP, parsed.QName, err)
	}
	pendingCount := uint64(len(c.pending))
	c.stateMu.Unlock()
	c.sourceQuery(sourceID)

	updatePeak(&c.metrics.pendingPeak, pendingCount)
}

func (c *collector) handleClientResponse(
	sourceID string,
	msg *dnstap.Message,
) {
	c.metrics.clientResponses.Add(1)

	wire := msg.GetResponseMessage()
	if len(wire) == 0 {
		c.sourceError(sourceID)
		return
	}

	parsed, err := parseDNS(wire)
	if err != nil {
		c.sourceError(sourceID)
		log.Printf(
			"unable to parse CLIENT_RESPONSE source=%s err=%v",
			sourceID,
			err,
		)
		return
	}

	clientIP := net.IP(msg.GetQueryAddress()).String()
	if clientIP == "<nil>" {
		clientIP = ""
	}

	protocol := msg.GetSocketProtocol().String()

	key := makeKey(
		sourceID,
		clientIP,
		msg.GetQueryPort(),
		protocol,
		parsed,
	)

	responseTime := timestamp(
		msg.GetResponseTimeSec(),
		msg.GetResponseTimeNsec(),
	)

	now := time.Now().UTC()

	if responseTime.IsZero() {
		responseTime = now
	}

	c.stateMu.Lock()
	pending, matched := c.pending[key]
	if matched && pending.Event.QueryTime != nil && responseTime.Before(*pending.Event.QueryTime) {
		matched = false
	}
	if matched {
		delete(c.pending, key)
		terminal := recentTerminal{Outcome: outcomeResponse, At: now}
		c.recent[key] = terminal
		if err := c.pendingStore.appendDelete(key, pending.ID, terminal); err != nil {
			log.Printf("persist completed pending source=%s client=%s qname=%s err=%v", sourceID, clientIP, parsed.QName, err)
		}
	} else if terminal, ok := c.recent[key]; ok && now.Sub(terminal.At) <= recentTerminalTTL && terminal.Outcome != outcomeNoResponse {
		c.stateMu.Unlock()
		c.metrics.duplicatesSuppressed.Add(1)
		log.Printf("duplicate response dropped source=%s client=%s qname=%s dns_id=%d", sourceID, clientIP, parsed.QName, parsed.ID)
		return
	} else {
		terminal := recentTerminal{Outcome: outcomeUnmatchedResponse, At: now}
		c.recent[key] = terminal
		if err := c.pendingStore.appendDelete(key, 0, terminal); err != nil {
			log.Printf("persist unmatched terminal source=%s client=%s qname=%s err=%v", sourceID, clientIP, parsed.QName, err)
		}
	}
	c.stateMu.Unlock()
	c.sourceResponse(sourceID)

	var latencyUS *int64

	if matched && pending.Event.QueryTime != nil {
		value := responseTime.Sub(*pending.Event.QueryTime).Microseconds()

		if value >= 0 {
			latencyUS = &value
		}
	}

	responseTimeCopy := responseTime
	outcome := outcomeUnmatchedResponse
	var queryTime *time.Time
	eventTime := responseTime
	if matched {
		outcome = outcomeResponse
		queryTime = pending.Event.QueryTime
		eventTime = *queryTime
	}

	event := Event{
		EventTime:    eventTime,
		IngestedAt:   now,
		QueryTime:    queryTime,
		ResponseTime: &responseTimeCopy,
		Outcome:      outcome,

		TenantID: c.tenant,
		SourceID: sourceID,

		ClientIP:   clientIP,
		ClientPort: msg.GetQueryPort(),

		Protocol: protocol,

		QName:  parsed.QName,
		QType:  qtypeName(parsed.QType),
		QClass: qclassName(parsed.QClass),

		RCode: rcodeName(parsed.RCode),

		DNSID: parsed.ID,

		ResponseBytes: len(wire),
		AnswerCount:   parsed.AnswerCount,

		LatencyUS:    latencyUS,
		MatchedQuery: matched,
	}

	c.emitEvent(event)
}

func parseDNS(wire []byte) (parsedDNS, error) {
	var packet dns.Msg

	if err := packet.Unpack(wire); err != nil {
		return parsedDNS{}, err
	}

	if len(packet.Question) == 0 {
		return parsedDNS{}, fmt.Errorf("DNS message has no question")
	}

	q := packet.Question[0]

	return parsedDNS{
		ID:          packet.Id,
		QName:       normalizeQName(q.Name),
		QType:       q.Qtype,
		QClass:      q.Qclass,
		RCode:       packet.Rcode,
		AnswerCount: len(packet.Answer),
	}, nil
}

func normalizeQName(name string) string {
	return strings.TrimSuffix(
		strings.ToLower(strings.TrimSpace(name)),
		".",
	)
}

func makeKey(
	sourceID string,
	clientIP string,
	clientPort uint32,
	protocol string,
	d parsedDNS,
) string {
	return fmt.Sprintf(
		"%s|%s|%d|%s|%d|%s|%d",
		sourceID,
		clientIP,
		clientPort,
		protocol,
		d.ID,
		d.QName,
		d.QType,
	)
}

func timestamp(sec uint64, nsec uint32) time.Time {
	if sec == 0 {
		return time.Time{}
	}

	return time.Unix(
		int64(sec),
		int64(nsec),
	).UTC()
}

func qtypeName(value uint16) string {
	if name, ok := dns.TypeToString[value]; ok {
		return name
	}

	return fmt.Sprintf("TYPE%d", value)
}

func qclassName(value uint16) string {
	if name, ok := dns.ClassToString[value]; ok {
		return name
	}

	return fmt.Sprintf("CLASS%d", value)
}

func rcodeName(value int) string {
	if name, ok := dns.RcodeToString[value]; ok {
		return name
	}

	return fmt.Sprintf("RCODE%d", value)
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}

	return host
}

func parseCSVSet(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

func (c *collector) pendingJanitor() {
	interval := c.correlationTimeout / 4
	if interval > time.Second {
		interval = time.Second
	}
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case now := <-ticker.C:
			c.expirePending(now.UTC())
		case <-c.backgroundStop:
			return
		}
	}
}

func (c *collector) expirePending(now time.Time) int {
	expired := make([]Event, 0)
	c.stateMu.Lock()
	for c.expiryHead < len(c.expiryQueue) {
		item := c.expiryQueue[c.expiryHead]
		if item.ExpiresAt.After(now) {
			break
		}
		c.expiryHead++
		pending, ok := c.pending[item.Key]
		if !ok || pending.ID != item.ID {
			continue
		}
		delete(c.pending, item.Key)
		terminal := recentTerminal{Outcome: outcomeNoResponse, At: now}
		c.recent[item.Key] = terminal
		if err := c.pendingStore.appendDelete(item.Key, pending.ID, terminal); err != nil {
			log.Printf("persist expired pending source=%s client=%s qname=%s err=%v", pending.Event.SourceID, pending.Event.ClientIP, pending.Event.QName, err)
		}
		event := pending.Event
		event.IngestedAt = now
		expired = append(expired, event)
	}
	if c.expiryHead >= 65536 && c.expiryHead*2 >= len(c.expiryQueue) {
		remaining := append([]expiryEntry(nil), c.expiryQueue[c.expiryHead:]...)
		c.expiryQueue = remaining
		c.expiryHead = 0
	}
	for key, terminal := range c.recent {
		if now.Sub(terminal.At) > recentTerminalTTL {
			delete(c.recent, key)
		}
	}
	c.stateMu.Unlock()

	for _, event := range expired {
		c.emitEvent(event)
	}
	return len(expired)
}

func (c *collector) emitEvent(event Event) {
	c.metrics.transactionsEmitted.Add(1)
	switch event.Outcome {
	case outcomeResponse:
		c.metrics.matchedResponses.Add(1)
	case outcomeNoResponse:
		c.metrics.noResponses.Add(1)
	case outcomeUnmatchedResponse:
		c.metrics.unmatchedResponses.Add(1)
	}
	c.sourceTransaction(event.SourceID, event.Outcome)

	if c.emitHook != nil {
		c.emitHook(event)
		return
	}
	if c.writer != nil && !c.writer.Enqueue(event) {
		log.Printf("storage queue full event dropped source=%s client=%s qname=%s", event.SourceID, event.ClientIP, event.QName)
	}
	if c.live != nil {
		c.live.Enqueue(event)
	}
	if c.debugEvents {
		c.outputMu.Lock()
		err := c.encoder.Encode(event)
		c.outputMu.Unlock()
		if err != nil {
			log.Printf("encode event: %v", err)
		}
	}
}

func updatePeak(counter *atomic.Uint64, value uint64) {
	for current := counter.Load(); value > current; current = counter.Load() {
		if counter.CompareAndSwap(current, value) {
			return
		}
	}
}

func (c *collector) connectionStarted(key, remote string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	source := c.sources[key]
	if source == nil {
		source = &sourceRuntime{Identity: key, RemoteAddress: remote}
		c.sources[key] = source
	}
	source.ActiveConnections++
	source.ConnectedAt = time.Now().UTC()
	source.RemoteAddress = remote
}

func (c *collector) connectionEnded(key string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	if source := c.sources[key]; source != nil && source.ActiveConnections > 0 {
		source.ActiveConnections--
	}
}

func (c *collector) sourceMessage(key, identity, remote string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	source := c.sources[key]
	if source == nil {
		source = &sourceRuntime{}
		c.sources[key] = source
	}
	source.Identity = identity
	source.RemoteAddress = remote
	source.LastMessageAt = time.Now().UTC()
	source.SeenSinceStart = true
	source.Frames++
	_, source.Expected = c.expectedSources[identity]
}

func (c *collector) sourceQuery(identity string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	if source := c.sourceByIdentityLocked(identity); source != nil {
		source.Queries++
	}
}

func (c *collector) sourceResponse(identity string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	if source := c.sourceByIdentityLocked(identity); source != nil {
		source.Responses++
	}
}

func (c *collector) sourceError(identity string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	if source := c.sourceByIdentityLocked(identity); source != nil {
		source.Errors++
	}
}

func (c *collector) sourceTransaction(identity, outcome string) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	if source := c.sourceByIdentityLocked(identity); source != nil {
		source.Transactions++
		if outcome == outcomeNoResponse {
			source.NoResponse++
		}
		if outcome == outcomeUnmatchedResponse {
			source.Unmatched++
		}
	}
}

func (c *collector) sourceByIdentityLocked(identity string) *sourceRuntime {
	for _, source := range c.sources {
		if source.Identity == identity {
			return source
		}
	}
	return nil
}

func (c *collector) recordPersisted(events []Event) {
	c.sourceMu.Lock()
	defer c.sourceMu.Unlock()
	for _, event := range events {
		for _, source := range c.sources {
			if source.Identity == event.SourceID && event.IngestedAt.After(source.LastPersistedAt) {
				source.LastPersistedAt = event.IngestedAt
			}
		}
	}
}

func (c *collector) sampleRates() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	last := c.metrics.transactionsEmitted.Load()
	for {
		select {
		case <-ticker.C:
			current := c.metrics.transactionsEmitted.Load()
			rate := float64(current-last) / 10
			c.metrics.normalizedRateBits.Store(math.Float64bits(rate))
			c.sourceMu.Lock()
			for _, source := range c.sources {
				source.Rate = float64(source.Transactions-source.lastRateTotal) / 10
				source.lastRateTotal = source.Transactions
			}
			c.sourceMu.Unlock()
			last = current
		case <-c.backgroundStop:
			return
		}
	}
}

type collectorStatus struct {
	ProcessStartedAt     time.Time                  `json:"process_started_at"`
	UptimeSeconds        int64                      `json:"uptime_seconds"`
	CorrelationTimeoutMS int64                      `json:"correlation_timeout_ms"`
	CountersSinceStart   map[string]uint64          `json:"counters_since_start"`
	ActiveConnections    int64                      `json:"active_dnstap_connections"`
	PendingCount         int                        `json:"pending_count"`
	PendingPeak          uint64                     `json:"pending_peak_since_start"`
	PendingPersistence   pendingPersistenceSnapshot `json:"pending_persistence"`
	NormalizedRate       float64                    `json:"normalized_rate_per_sec"`
	LastDNSTapEvent      *time.Time                 `json:"last_dnstap_event_time"`
	Persistence          clickhouseWriterSnapshot   `json:"persistence"`
	Live                 livePublisherSnapshot      `json:"live"`
	Sources              []sourceRuntime            `json:"sources"`
	Time                 time.Time                  `json:"time"`
}

func (c *collector) statusSnapshot(now time.Time) collectorStatus {
	c.stateMu.Lock()
	pendingCount := len(c.pending)
	c.stateMu.Unlock()
	c.sourceMu.RLock()
	sources := make([]sourceRuntime, 0, len(c.sources))
	seenExpected := make(map[string]struct{})
	for _, source := range c.sources {
		copySource := *source
		if _, ok := c.expectedSources[copySource.Identity]; ok {
			copySource.Expected = true
			seenExpected[copySource.Identity] = struct{}{}
		}
		sources = append(sources, copySource)
	}
	for expected := range c.expectedSources {
		if _, ok := seenExpected[expected]; !ok {
			sources = append(sources, sourceRuntime{Identity: expected, Expected: true})
		}
	}
	c.sourceMu.RUnlock()
	sort.Slice(sources, func(i, j int) bool { return sources[i].Identity < sources[j].Identity })
	var persistence clickhouseWriterSnapshot
	if c.writer != nil {
		persistence = c.writer.snapshot()
	}
	var live livePublisherSnapshot
	if c.live != nil {
		live = c.live.snapshot()
	}
	return collectorStatus{
		ProcessStartedAt:     c.startedAt,
		UptimeSeconds:        int64(now.Sub(c.startedAt).Seconds()),
		CorrelationTimeoutMS: c.correlationTimeout.Milliseconds(),
		CountersSinceStart: map[string]uint64{
			"frames_received":                c.metrics.framesReceived.Load(),
			"client_queries_received":        c.metrics.clientQueries.Load(),
			"client_responses_received":      c.metrics.clientResponses.Load(),
			"transactions_emitted":           c.metrics.transactionsEmitted.Load(),
			"matched_responses":              c.metrics.matchedResponses.Load(),
			"no_response":                    c.metrics.noResponses.Load(),
			"unmatched_responses":            c.metrics.unmatchedResponses.Load(),
			"duplicate_responses_suppressed": c.metrics.duplicatesSuppressed.Load(),
		},
		ActiveConnections:  c.metrics.activeConnections.Load(),
		PendingCount:       pendingCount,
		PendingPeak:        c.metrics.pendingPeak.Load(),
		PendingPersistence: c.pendingStore.snapshot(now),
		NormalizedRate:     math.Float64frombits(c.metrics.normalizedRateBits.Load()),
		LastDNSTapEvent:    timeFromUnixNano(c.metrics.lastDNSTapEventNS.Load()),
		Persistence:        persistence,
		Live:               live,
		Sources:            sources,
		Time:               now,
	}
}

func (c *collector) pendingCheckpointLoop() {
	ticker := time.NewTicker(c.pendingStore.checkpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.checkpointPending(); err != nil {
				c.pendingStore.writeErrors.Add(1)
				log.Printf("checkpoint pending state: %v", err)
			}
		case <-c.backgroundStop:
			return
		}
	}
}

func (c *collector) checkpointPending() error {
	if c.pendingStore == nil {
		return nil
	}
	c.checkpointMu.Lock()
	defer c.checkpointMu.Unlock()
	c.stateMu.Lock()
	sequence, err := c.pendingStore.prepareCheckpoint()
	if err != nil {
		c.stateMu.Unlock()
		return err
	}
	state := pendingCheckpoint{
		NextPendingID: c.nextPendingID,
		Pending:       copyPendingMap(c.pending),
		Recent:        copyRecentMap(c.recent),
	}
	c.stateMu.Unlock()
	return c.pendingStore.installCheckpoint(state, sequence)
}

func (c *collector) closePendingStore() {
	if c.pendingStore == nil {
		return
	}
	if err := c.checkpointPending(); err != nil {
		log.Printf("final pending checkpoint: %v", err)
		_ = c.pendingStore.close(false)
		return
	}
	if err := c.pendingStore.close(true); err != nil {
		log.Printf("close pending persistence: %v", err)
	}
}

func timeFromUnixNano(value int64) *time.Time {
	if value == 0 {
		return nil
	}
	t := time.Unix(0, value).UTC()
	return &t
}

func (c *collector) startStatusServer(addr string) *http.Server {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen collector status %s: %v", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(c.statusSnapshot(time.Now().UTC())); err != nil {
			log.Printf("encode collector status: %v", err)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("collector status server: %v", err)
		}
	}()
	return server
}
