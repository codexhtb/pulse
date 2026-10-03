package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

type apiRuntimeMetrics struct {
	requests       atomic.Uint64
	errors         atomic.Uint64
	totalLatencyUS atomic.Uint64
	maxLatencyUS   atomic.Uint64
	lastErrorNS    atomic.Int64
}

type apiStatus struct {
	ProcessStartedAt   time.Time          `json:"process_started_at"`
	UptimeSeconds      int64              `json:"uptime_seconds"`
	CountersSinceStart map[string]uint64  `json:"counters_since_start"`
	RequestLatency     map[string]float64 `json:"request_latency_ms"`
	LastErrorAt        *time.Time         `json:"last_error_at"`
}

type collectorPersistenceStatus struct {
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

type collectorLiveStatus struct {
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

type collectorSourceStatus struct {
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
}

type collectorPendingPersistenceStatus struct {
	Enabled          bool       `json:"enabled"`
	WALBytes         int64      `json:"wal_bytes"`
	CheckpointBytes  int64      `json:"checkpoint_bytes"`
	LastCheckpointAt *time.Time `json:"last_checkpoint_at"`
	LastSyncAt       *time.Time `json:"last_sync_at"`
	RecoveredPending uint64     `json:"recovered_pending"`
	RecoveryErrors   uint64     `json:"recovery_errors"`
	WriteErrors      uint64     `json:"write_errors"`
	TruncatedBytes   uint64     `json:"truncated_tail_bytes"`
	PersistenceLagMS int64      `json:"persistence_lag_ms"`
	UncleanRecovery  bool       `json:"unclean_recovery"`
	FlushIntervalMS  int64      `json:"flush_interval_ms"`
	SyncIntervalMS   int64      `json:"sync_interval_ms"`
	CheckpointMS     int64      `json:"checkpoint_interval_ms"`
}

type collectorRuntimeStatus struct {
	ProcessStartedAt     time.Time                         `json:"process_started_at"`
	UptimeSeconds        int64                             `json:"uptime_seconds"`
	CorrelationTimeoutMS int64                             `json:"correlation_timeout_ms"`
	CountersSinceStart   map[string]uint64                 `json:"counters_since_start"`
	ActiveConnections    int64                             `json:"active_dnstap_connections"`
	PendingCount         int                               `json:"pending_count"`
	PendingPeak          uint64                            `json:"pending_peak_since_start"`
	PendingPersistence   collectorPendingPersistenceStatus `json:"pending_persistence"`
	NormalizedRate       float64                           `json:"normalized_rate_per_sec"`
	LastDNSTapEvent      *time.Time                        `json:"last_dnstap_event_time"`
	Persistence          collectorPersistenceStatus        `json:"persistence"`
	Live                 collectorLiveStatus               `json:"live"`
	Sources              []collectorSourceStatus           `json:"sources"`
	Time                 time.Time                         `json:"time"`
}

type apiLiveStatus struct {
	Subscribers       int    `json:"sse_subscribers"`
	UDPEventsReceived uint64 `json:"udp_events_received"`
	DecodeErrors      uint64 `json:"decode_errors"`
}

type storageStatus struct {
	RawRows                    uint64     `json:"raw_rows"`
	RawCompressedBytes         *uint64    `json:"raw_compressed_bytes,omitempty"`
	DatabaseCompressedBytes    *uint64    `json:"database_compressed_bytes,omitempty"`
	OldestRawEvent             *time.Time `json:"oldest_raw_event"`
	NewestRawEvent             *time.Time `json:"newest_raw_event"`
	ActiveParts                *uint64    `json:"active_parts,omitempty"`
	ActiveMerges               *uint64    `json:"active_merges,omitempty"`
	DiskTotalBytes             *uint64    `json:"disk_total_bytes,omitempty"`
	DiskFreeBytes              *uint64    `json:"disk_free_bytes,omitempty"`
	DiskUsedBytes              *uint64    `json:"disk_used_bytes,omitempty"`
	FilesystemPath             string     `json:"filesystem_path,omitempty"`
	FilesystemUtilizationPct   *float64   `json:"filesystem_utilization_pct,omitempty"`
	FilesystemStatus           string     `json:"filesystem_status,omitempty"`
	FilesystemWarningPct       *float64   `json:"filesystem_warning_pct,omitempty"`
	FilesystemCriticalPct      *float64   `json:"filesystem_critical_pct,omitempty"`
	OptionalMetricsUnavailable []string   `json:"optional_metrics_unavailable,omitempty"`
}

type persistedSource struct {
	SourceID string
	LastSeen time.Time
}

type systemSource struct {
	SourceID        string           `json:"source_id"`
	State           string           `json:"state"`
	Connected       bool             `json:"connected"`
	RemoteAddress   string           `json:"remote_address,omitempty"`
	LastMessageAt   *time.Time       `json:"last_message_at"`
	LastPersistedAt *time.Time       `json:"last_persisted_at"`
	AgeSeconds      *int64           `json:"age_seconds"`
	Expected        bool             `json:"expected"`
	Unexpected      bool             `json:"unexpected"`
	SeenSinceStart  bool             `json:"seen_since_start"`
	Frames          uint64           `json:"frames_received"`
	Queries         uint64           `json:"queries_received"`
	Responses       uint64           `json:"responses_received"`
	Transactions    uint64           `json:"transactions_emitted"`
	NoResponse      uint64           `json:"no_response"`
	Unmatched       uint64           `json:"unmatched_responses"`
	Errors          uint64           `json:"errors"`
	Rate            float64          `json:"transactions_per_sec"`
	Control         *controlNodeView `json:"control,omitempty"`
}

type systemResponse struct {
	Status      string                     `json:"status"`
	Subsystems  map[string]string          `json:"subsystems"`
	Collector   collectorRuntimeStatus     `json:"collector"`
	Persistence collectorPersistenceStatus `json:"persistence"`
	Live        struct {
		Publisher collectorLiveStatus `json:"publisher"`
		API       apiLiveStatus       `json:"api"`
	} `json:"live"`
	API          apiStatus         `json:"api"`
	Storage      storageStatus     `json:"storage"`
	Sources      []systemSource    `json:"sources"`
	ControlNodes []controlNodeView `json:"control_nodes"`
	Time         time.Time         `json:"time"`
}

func (s *server) system(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	collector, err := s.fetchCollectorStatus(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "critical", "error": "collector status unavailable", "time": now})
		return
	}
	loader := s.storageSnapshot
	if s.systemStorage != nil {
		loader = s.systemStorage
	}
	storage, persisted, err := loader(ctx)
	if err != nil {
		writeDBError(w, err)
		return
	}
	sources := mergeSystemSources(now, time.Duration(collector.CorrelationTimeoutMS)*time.Millisecond, collector.Sources, persisted)
	var controlNodes []controlNodeView
	if s.control != nil {
		controlNodes = s.control.nodeViews()
		sources = attachControlNodes(sources, controlNodes)
	}
	out := systemResponse{
		Collector: collector, Persistence: collector.Persistence,
		API: s.apiStatusSnapshot(now), Storage: storage, Sources: sources, ControlNodes: controlNodes, Time: now,
	}
	out.Live.Publisher = collector.Live
	out.Live.API = apiLiveStatus{Subscribers: s.live.subscriberCount(), UDPEventsReceived: s.live.received.Load(), DecodeErrors: s.live.decodeErrors.Load()}
	out.Status, out.Subsystems = evaluateSystemStatus(now, collector, out.Live.API, storage, sources)
	controlStatus := evaluateControlStatus(controlNodes)
	out.Subsystems["control"] = controlStatus
	out.Status = combineStatus(out.Status, controlStatus)
	writeJSON(w, http.StatusOK, out)
}

func (s *server) fetchCollectorStatus(ctx context.Context) (collectorRuntimeStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.collectorStatusURL, nil)
	if err != nil {
		return collectorRuntimeStatus{}, err
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return collectorRuntimeStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return collectorRuntimeStatus{}, fmt.Errorf("collector status HTTP %d", response.StatusCode)
	}
	var out collectorRuntimeStatus
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func (s *server) storageSnapshot(ctx context.Context) (storageStatus, []persistedSource, error) {
	var out storageStatus
	err := s.db.QueryRow(ctx, `SELECT toUInt64(count()) FROM pulse.dns_events`).Scan(&out.RawRows)
	if err != nil {
		return out, nil, err
	}
	if s.clickhouseSystemMetrics {
		var rawRowsFromParts, rawBytes, databaseBytes, activeParts uint64
		if err := s.db.QueryRow(ctx, `SELECT
			toUInt64(sumIf(rows, table = 'dns_events')),
			toUInt64(sumIf(data_compressed_bytes, table = 'dns_events')),
			toUInt64(sum(data_compressed_bytes)), toUInt64(count())
			FROM system.parts WHERE active AND database = 'pulse'`).Scan(&rawRowsFromParts, &rawBytes, &databaseBytes, &activeParts); err == nil {
			out.RawCompressedBytes = &rawBytes
			out.DatabaseCompressedBytes = &databaseBytes
			out.ActiveParts = &activeParts
		} else {
			out.OptionalMetricsUnavailable = append(out.OptionalMetricsUnavailable, "raw_compressed_bytes", "database_compressed_bytes", "active_parts")
		}
	} else {
		out.OptionalMetricsUnavailable = append(out.OptionalMetricsUnavailable, "raw_compressed_bytes", "database_compressed_bytes", "active_parts")
	}
	var oldest, newest *time.Time
	if out.RawRows > 0 {
		var a, b time.Time
		if err := s.db.QueryRow(ctx, `SELECT min(event_time), max(event_time) FROM pulse.dns_events`).Scan(&a, &b); err != nil {
			return out, nil, err
		}
		oldest, newest = &a, &b
	}
	out.OldestRawEvent, out.NewestRawEvent = oldest, newest
	if s.clickhouseSystemMetrics {
		var merges uint64
		if err := s.db.QueryRow(ctx, `SELECT toUInt64(count()) FROM system.merges WHERE database = 'pulse'`).Scan(&merges); err == nil {
			out.ActiveMerges = &merges
		} else {
			out.OptionalMetricsUnavailable = append(out.OptionalMetricsUnavailable, "active_merges")
		}
	} else {
		out.OptionalMetricsUnavailable = append(out.OptionalMetricsUnavailable, "active_merges")
	}
	if s.storagePath != "" {
		var stat unix.Statfs_t
		if err := unix.Statfs(s.storagePath, &stat); err == nil {
			total := uint64(stat.Blocks) * uint64(stat.Bsize)
			free := uint64(stat.Bavail) * uint64(stat.Bsize)
			used := total - free
			utilization := 0.0
			if total > 0 {
				utilization = 100 * float64(used) / float64(total)
			}
			status := "healthy"
			if utilization >= s.storageCriticalPct {
				status = "critical"
			} else if utilization >= s.storageWarningPct {
				status = "warning"
			}
			warning, critical := s.storageWarningPct, s.storageCriticalPct
			out.DiskTotalBytes, out.DiskFreeBytes, out.DiskUsedBytes = &total, &free, &used
			out.FilesystemPath, out.FilesystemUtilizationPct, out.FilesystemStatus = s.storagePath, &utilization, status
			out.FilesystemWarningPct, out.FilesystemCriticalPct = &warning, &critical
		} else {
			out.OptionalMetricsUnavailable = append(out.OptionalMetricsUnavailable, "filesystem_space")
		}
	} else {
		out.OptionalMetricsUnavailable = append(out.OptionalMetricsUnavailable, "filesystem_space")
	}
	rows, err := s.db.Query(ctx, `SELECT source_id, maxMerge(last_seen_state) FROM pulse.source_last_seen WHERE tenant_id=? GROUP BY source_id`, s.tenant)
	if err != nil {
		return out, nil, err
	}
	defer rows.Close()
	persisted := make([]persistedSource, 0)
	for rows.Next() {
		var item persistedSource
		if err := rows.Scan(&item.SourceID, &item.LastSeen); err != nil {
			return out, nil, err
		}
		persisted = append(persisted, item)
	}
	return out, persisted, rows.Err()
}

func mergeSystemSources(now time.Time, timeout time.Duration, runtime []collectorSourceStatus, persisted []persistedSource) []systemSource {
	byID := make(map[string]systemSource)
	for _, source := range runtime {
		item := systemSource{
			SourceID: source.Identity, Connected: source.ActiveConnections > 0, RemoteAddress: source.RemoteAddress,
			Expected: source.Expected, Unexpected: !source.Expected, SeenSinceStart: source.SeenSinceStart,
			Frames: source.Frames, Queries: source.Queries, Responses: source.Responses, Transactions: source.Transactions,
			NoResponse: source.NoResponse, Unmatched: source.Unmatched, Errors: source.Errors, Rate: source.Rate,
		}
		if !source.LastMessageAt.IsZero() {
			t := source.LastMessageAt
			item.LastMessageAt = &t
		}
		if !source.LastPersistedAt.IsZero() {
			t := source.LastPersistedAt
			item.LastPersistedAt = &t
		}
		byID[item.SourceID] = item
	}
	for _, source := range persisted {
		item := byID[source.SourceID]
		item.SourceID = source.SourceID
		if _, exists := byID[source.SourceID]; !exists {
			item.Unexpected = true
		}
		if item.LastPersistedAt == nil || source.LastSeen.After(*item.LastPersistedAt) {
			t := source.LastSeen
			item.LastPersistedAt = &t
		}
		byID[source.SourceID] = item
	}
	silence := 2 * timeout
	if silence < 60*time.Second {
		silence = 60 * time.Second
	}
	result := make([]systemSource, 0, len(byID))
	for _, item := range byID {
		latest := item.LastMessageAt
		if latest == nil {
			latest = item.LastPersistedAt
		}
		if latest != nil {
			age := int64(now.Sub(*latest).Seconds())
			if age < 0 {
				age = 0
			}
			item.AgeSeconds = &age
		}
		switch {
		case item.Expected && !item.SeenSinceStart && !item.Connected:
			item.State = "never_seen_since_start"
		case item.Connected && latest == nil:
			item.State = "connected_no_events"
		case item.Connected && now.Sub(*latest) <= silence:
			item.State = "active"
		case item.Connected:
			item.State = "silent"
		default:
			item.State = "disconnected"
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceID < result[j].SourceID })
	return result
}

func attachControlNodes(sources []systemSource, nodes []controlNodeView) []systemSource {
	byIdentity := make(map[string]int, len(sources))
	for index := range sources {
		byIdentity[sources[index].SourceID] = index
	}
	for index := range nodes {
		node := nodes[index]
		if sourceIndex, exists := byIdentity[node.SourceIdentity]; exists {
			sources[sourceIndex].Control = &node
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].SourceID < sources[j].SourceID })
	return sources
}

func evaluateSystemStatus(now time.Time, collector collectorRuntimeStatus, live apiLiveStatus, storage storageStatus, sources []systemSource) (string, map[string]string) {
	durable, liveState, sourceState, storageState := "healthy", "healthy", "healthy", "healthy"
	p := collector.Persistence
	util := 0.0
	if p.QueueCapacity > 0 {
		util = float64(p.QueueDepth) / float64(p.QueueCapacity)
	}
	if p.DurableDropped > 0 || util >= 0.95 {
		durable = "critical"
	} else if util >= 0.75 || (p.LastFailureAt != nil && now.Sub(*p.LastFailureAt) <= 5*time.Minute) {
		durable = "degraded"
	}
	pp := collector.PendingPersistence
	if !pp.Enabled && durable == "healthy" {
		durable = "degraded"
	}
	if pp.WriteErrors > 0 {
		durable = "critical"
	} else if pp.RecoveryErrors > 0 && durable == "healthy" {
		durable = "degraded"
	}
	if collector.Live.Dropped > 0 || collector.Live.UDPErrors > 0 || live.DecodeErrors > 0 {
		liveState = "degraded"
	}
	expected, badExpected, unexpected, bad := 0, 0, 0, 0
	for _, source := range sources {
		if source.State != "active" {
			bad++
		}
		if source.Expected {
			expected++
			if source.State != "active" {
				badExpected++
			}
		} else if source.Unexpected {
			unexpected++
		}
	}
	if expected > 0 {
		if badExpected == expected {
			sourceState = "critical"
		} else if badExpected > 0 || unexpected > 0 {
			sourceState = "degraded"
		}
	} else {
		if len(sources) == 0 || bad == len(sources) {
			sourceState = "critical"
		} else if bad > 0 {
			sourceState = "degraded"
		}
	}
	if strings.EqualFold(storage.FilesystemStatus, "critical") {
		storageState = "critical"
	} else if strings.EqualFold(storage.FilesystemStatus, "warning") {
		storageState = "degraded"
	}
	overall := "healthy"
	for _, state := range []string{durable, sourceState, storageState} {
		if state == "critical" {
			overall = "critical"
			break
		}
		if state == "degraded" {
			overall = "degraded"
		}
	}
	if overall == "healthy" && liveState == "degraded" {
		overall = "degraded"
	}
	return overall, map[string]string{"durable": durable, "live": liveState, "sources": sourceState, "storage": storageState}
}

func evaluateControlStatus(nodes []controlNodeView) string {
	enabled, bad := 0, 0
	for _, node := range nodes {
		if !node.ControlEnabled {
			continue
		}
		enabled++
		if node.Health.Status != "ok" {
			bad++
		}
	}
	if enabled == 0 {
		return "disabled"
	}
	if bad == enabled {
		return "critical"
	}
	if bad > 0 {
		return "degraded"
	}
	return "healthy"
}

func combineStatus(overall, subsystem string) string {
	if subsystem == "critical" || overall == "critical" {
		return "critical"
	}
	if subsystem == "degraded" || overall == "degraded" {
		return "degraded"
	}
	return overall
}

func (s *server) apiStatusSnapshot(now time.Time) apiStatus {
	requests := s.metrics.requests.Load()
	total := s.metrics.totalLatencyUS.Load()
	avg := 0.0
	if requests > 0 {
		avg = float64(total) / float64(requests) / 1000
	}
	return apiStatus{ProcessStartedAt: s.startedAt, UptimeSeconds: int64(now.Sub(s.startedAt).Seconds()), CountersSinceStart: map[string]uint64{"requests": requests, "errors": s.metrics.errors.Load()}, RequestLatency: map[string]float64{"average": finite(avg), "maximum": finite(float64(s.metrics.maxLatencyUS.Load()) / 1000)}, LastErrorAt: timePtr(s.metrics.lastErrorNS.Load())}
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
func timePtr(ns int64) *time.Time {
	if ns == 0 {
		return nil
	}
	t := time.Unix(0, ns).UTC()
	return &t
}
func updateMax(counter *atomic.Uint64, value uint64) {
	for current := counter.Load(); value > current; current = counter.Load() {
		if counter.CompareAndSwap(current, value) {
			return
		}
	}
}
