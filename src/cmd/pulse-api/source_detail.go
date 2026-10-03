package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type sourceDetailResponse struct {
	Range               string     `json:"range"`
	SourceID            string     `json:"source_id"`
	Expected            bool       `json:"expected"`
	Unexpected          bool       `json:"unexpected"`
	State               string     `json:"state"`
	Connected           bool       `json:"connected"`
	RemoteAddress       string     `json:"remote_address,omitempty"`
	LastMessageAt       *time.Time `json:"last_message_at"`
	LastPersistedAt     *time.Time `json:"last_persisted_at"`
	AgeSeconds          *int64     `json:"age_seconds"`
	IngestRate          float64    `json:"ingest_rate_per_sec"`
	FramesSinceStart    uint64     `json:"frames_since_start"`
	QueriesSinceStart   uint64     `json:"queries_since_start"`
	ResponsesSinceStart uint64     `json:"responses_since_start"`
	ErrorsSinceStart    uint64     `json:"errors_since_start"`
	Queries             uint64     `json:"queries"`
	Responses           uint64     `json:"responses"`
	NoResponse          uint64     `json:"no_response"`
	UnmatchedResponses  uint64     `json:"unmatched_responses"`
	NXDomain            uint64     `json:"nxdomain"`
	Servfail            uint64     `json:"servfail"`
	AvgLatencyUS        float64    `json:"avg_latency_us"`
}

func (s *server) sourceRoute(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/sources/"), "/")
	if tail == "" || strings.Contains(tail, "/") {
		http.NotFound(w, r)
		return
	}
	sourceID, err := url.PathUnescape(tail)
	if err != nil || strings.TrimSpace(sourceID) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid source path"))
		return
	}
	rangeName, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var out sourceDetailResponse
	out.Range, out.SourceID = rangeName, sourceID
	if err := s.db.QueryRow(ctx, `SELECT sum(total_queries), sum(responses), sum(no_response),
		sum(unmatched_responses), sum(nxdomain), sum(servfail),
		if(sum(latency_samples)=0,0,sum(latency_us_sum)/sum(latency_samples))
		FROM pulse.traffic_minute WHERE tenant_id=? AND source_id=? AND minute>=?`,
		s.tenant, sourceID, time.Now().UTC().Add(-duration)).Scan(
		&out.Queries, &out.Responses, &out.NoResponse, &out.UnmatchedResponses,
		&out.NXDomain, &out.Servfail, &out.AvgLatencyUS,
	); err != nil {
		writeDBError(w, err)
		return
	}
	var persisted []persistedSource
	var lastSeen time.Time
	var persistedRows uint64
	if err := s.db.QueryRow(ctx, `SELECT count(), maxMerge(last_seen_state) FROM pulse.source_last_seen
		WHERE tenant_id=? AND source_id=?`, s.tenant, sourceID).Scan(&persistedRows, &lastSeen); err != nil {
		writeDBError(w, err)
		return
	}
	if persistedRows > 0 {
		persisted = append(persisted, persistedSource{SourceID: sourceID, LastSeen: lastSeen})
	}
	collector, err := s.fetchCollectorStatus(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "collector status unavailable"})
		return
	}
	runtime := make([]collectorSourceStatus, 0, 1)
	for _, source := range collector.Sources {
		if source.Identity == sourceID {
			runtime = append(runtime, source)
			out.IngestRate = source.Rate
			out.FramesSinceStart = source.Frames
			out.QueriesSinceStart = source.Queries
			out.ResponsesSinceStart = source.Responses
			out.ErrorsSinceStart = source.Errors
		}
	}
	merged := mergeSystemSources(time.Now().UTC(), time.Duration(collector.CorrelationTimeoutMS)*time.Millisecond, runtime, persisted)
	if len(merged) == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("source not found"))
		return
	}
	item := merged[0]
	out.Expected, out.Unexpected, out.State, out.Connected = item.Expected, item.Unexpected, item.State, item.Connected
	out.RemoteAddress, out.LastMessageAt, out.LastPersistedAt, out.AgeSeconds = item.RemoteAddress, item.LastMessageAt, item.LastPersistedAt, item.AgeSeconds
	writeJSON(w, http.StatusOK, out)
}
