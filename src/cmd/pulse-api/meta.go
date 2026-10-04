package main

import (
	"context"
	"net/http"
	"time"
)

type anomaly struct {
	Type       string  `json:"type"`
	Severity   string  `json:"severity"`
	Title      string  `json:"title"`
	Value      float64 `json:"value"`
	Threshold  float64 `json:"threshold"`
	Unit       string  `json:"unit"`
	SourceID   string  `json:"source_id,omitempty"`
	ObservedAt string  `json:"observed_at"`
}

func (s *server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "Pulse",
		"product": "DNS Traffic Visibility",
		"api":     "v1",

		"ranges": []string{
			"15m",
			"1h",
			"3h",
			"6h",
			"12h",
			"24h",
		},

		"retention": map[string]string{
			"raw":                   rawEventRetentionLabel,
			"domain_minute":         "90d",
			"client_minute":         "90d",
			"client_domain_hour":    "90d",
			"client_latency_minute": "90d",
			"domain_latency_minute": "90d",
			"traffic_minute":        "365d",
			"rcode_minute":          "365d",
			"latency_minute":        "365d",
		},

		"capabilities": map[string]bool{
			"overview":             true,
			"search":               true,
			"cursor_search":        true,
			"client_detail":        true,
			"domain_detail":        true,
			"client_domains":       true,
			"domain_clients":       true,
			"source_detail":        true,
			"live_sse":             true,
			"live_outcome_filters": true,
			"anomalies":            true,
			"system":               true,
			"transaction_outcomes": true,
			"multi_source":         true,
			"multi_tenant":         true,
			"dns_block_control":    s.control != nil,
			"multi_dns_control":    s.control != nil,
		},

		"live": map[string]any{
			"transport":        "sse",
			"default_rate":     200,
			"maximum_rate":     1000,
			"server_filtering": true,
		},

		"time": time.Now().UTC(),
	})
}

func (s *server) anomalies(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	out := make([]anomaly, 0)

	var queries uint64
	var responses uint64
	var noResponse uint64
	var nxdomain uint64
	var servfail uint64
	var avgLatency float64

	err := s.db.QueryRow(ctx, `
		SELECT
			sum(total_queries),
			sum(responses),
			sum(no_response),
			sum(nxdomain),
			sum(servfail),
			if(
				sum(latency_samples) = 0,
				0,
				sum(latency_us_sum) / sum(latency_samples)
			)
		FROM pulse.traffic_minute
		WHERE tenant_id = ?
		  AND minute >= now() - INTERVAL 15 MINUTE
	`, s.tenant).Scan(
		&queries,
		&responses,
		&noResponse,
		&nxdomain,
		&servfail,
		&avgLatency,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	if responses > 0 {
		nxPct := 100 * float64(nxdomain) / float64(responses)

		if nxPct >= 20 {
			severity := "warning"
			if nxPct >= 40 {
				severity = "critical"
			}

			out = append(out, anomaly{
				Type:       "high_nxdomain_rate",
				Severity:   severity,
				Title:      "Elevated NXDOMAIN rate",
				Value:      nxPct,
				Threshold:  20,
				Unit:       "percent",
				ObservedAt: now,
			})
		}

		sfPct := 100 * float64(servfail) / float64(responses)

		if sfPct >= 2 {
			severity := "warning"
			if sfPct >= 10 {
				severity = "critical"
			}

			out = append(out, anomaly{
				Type:       "high_servfail_rate",
				Severity:   severity,
				Title:      "Elevated SERVFAIL rate",
				Value:      sfPct,
				Threshold:  2,
				Unit:       "percent",
				ObservedAt: now,
			})
		}
	}

	if queries > 0 {
		noResponsePct := 100 * float64(noResponse) / float64(queries)
		if noResponsePct >= 5 {
			severity := "warning"
			if noResponsePct >= 20 {
				severity = "critical"
			}
			out = append(out, anomaly{Type: "high_no_response_rate", Severity: severity, Title: "Client responses not observed", Value: noResponsePct, Threshold: 5, Unit: "percent", ObservedAt: now})
		}
	}

	if avgLatency >= 100000 {
		severity := "warning"

		if avgLatency >= 500000 {
			severity = "critical"
		}

		out = append(out, anomaly{
			Type:       "high_average_latency",
			Severity:   severity,
			Title:      "Elevated DNS response latency",
			Value:      avgLatency / 1000,
			Threshold:  100,
			Unit:       "ms",
			ObservedAt: now,
		})
	}

	rows, err := s.db.Query(ctx, `
		SELECT
			source_id,
			maxMerge(last_seen_state)
		FROM pulse.source_last_seen
		WHERE tenant_id = ?
		GROUP BY source_id
	`, s.tenant)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var source string
		var lastSeen time.Time

		if err := rows.Scan(&source, &lastSeen); err != nil {
			writeDBError(w, err)
			return
		}

		age := time.Since(lastSeen)

		if age >= 5*time.Minute {
			severity := "warning"

			if age >= 15*time.Minute {
				severity = "critical"
			}

			out = append(out, anomaly{
				Type:       "source_silent",
				Severity:   severity,
				Title:      "DNS source is silent",
				Value:      age.Minutes(),
				Threshold:  5,
				Unit:       "minutes",
				SourceID:   source,
				ObservedAt: now,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"window":    "15m",
		"count":     len(out),
		"anomalies": out,
	})
}
