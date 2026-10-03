package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type clientDetailResponse struct {
	Range              string         `json:"range"`
	ClientIP           string         `json:"client_ip"`
	Queries            uint64         `json:"queries"`
	Responses          uint64         `json:"responses"`
	NoResponse         uint64         `json:"no_response"`
	NoResponsePct      float64        `json:"no_response_pct"`
	UnmatchedResponses uint64         `json:"unmatched_responses"`
	UniqueDomains      uint64         `json:"unique_domains"`
	NXDomain           uint64         `json:"nxdomain"`
	NXDomainPct        float64        `json:"nxdomain_pct"`
	Servfail           uint64         `json:"servfail"`
	ServfailPct        float64        `json:"servfail_pct"`
	AvgLatencyUS       float64        `json:"avg_latency_us"`
	P50LatencyUS       float64        `json:"p50_latency_us"`
	P95LatencyUS       float64        `json:"p95_latency_us"`
	P99LatencyUS       float64        `json:"p99_latency_us"`
	ResponseBytes      uint64         `json:"response_bytes"`
	LastSeen           time.Time      `json:"last_seen"`
	State              string         `json:"state"`
	Sources            []entitySource `json:"sources"`
}

type domainDetailResponse struct {
	Range              string         `json:"range"`
	Domain             string         `json:"domain"`
	Queries            uint64         `json:"queries"`
	Responses          uint64         `json:"responses"`
	NoResponse         uint64         `json:"no_response"`
	NoResponsePct      float64        `json:"no_response_pct"`
	UnmatchedResponses uint64         `json:"unmatched_responses"`
	UniqueClients      uint64         `json:"unique_clients"`
	NXDomain           uint64         `json:"nxdomain"`
	NXDomainPct        float64        `json:"nxdomain_pct"`
	Servfail           uint64         `json:"servfail"`
	ServfailPct        float64        `json:"servfail_pct"`
	AvgLatencyUS       float64        `json:"avg_latency_us"`
	P50LatencyUS       float64        `json:"p50_latency_us"`
	P95LatencyUS       float64        `json:"p95_latency_us"`
	P99LatencyUS       float64        `json:"p99_latency_us"`
	ResponseBytes      uint64         `json:"response_bytes"`
	LastSeen           time.Time      `json:"last_seen"`
	State              string         `json:"state"`
	Sources            []entitySource `json:"sources"`
	QTypes             []qtypeRow     `json:"qtypes"`
}

type entitySource struct {
	SourceID string    `json:"source_id"`
	LastSeen time.Time `json:"last_seen"`
}

type investigationDomainRow struct {
	Domain        string `json:"domain"`
	Queries       uint64 `json:"queries"`
	Responses     uint64 `json:"responses"`
	NoResponse    uint64 `json:"no_response"`
	NXDomain      uint64 `json:"nxdomain"`
	Servfail      uint64 `json:"servfail"`
	ResponseBytes uint64 `json:"response_bytes"`
}

type investigationClientRow struct {
	ClientIP      string `json:"client_ip"`
	Queries       uint64 `json:"queries"`
	Responses     uint64 `json:"responses"`
	NoResponse    uint64 `json:"no_response"`
	NXDomain      uint64 `json:"nxdomain"`
	Servfail      uint64 `json:"servfail"`
	ResponseBytes uint64 `json:"response_bytes"`
}

type qtypeRow struct {
	QType   string `json:"qtype"`
	Queries uint64 `json:"queries"`
}

func (s *server) clientRoute(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/v1/clients/")
	tail = strings.Trim(tail, "/")

	if tail == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(tail, "/")

	rawIP, err := url.PathUnescape(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid client path"))
		return
	}

	ip := net.ParseIP(rawIP)
	if ip == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid client IP"))
		return
	}
	clientIP := ip.String()

	switch {
	case len(parts) == 1:
		s.clientDetail(w, r, clientIP)

	case len(parts) == 2 && parts[1] == "traffic":
		s.clientTraffic(w, r, clientIP)

	case len(parts) == 2 && parts[1] == "domains":
		s.clientDomains(w, r, clientIP)

	case len(parts) == 2 && parts[1] == "dns-block":
		controlIP, err := normalizeControlIP(clientIP)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		s.dnsBlockRoute(w, r, controlIP)

	default:
		http.NotFound(w, r)
	}
}

func (s *server) domainRoute(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/v1/domains/")
	tail = strings.Trim(tail, "/")

	if tail == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(tail, "/")

	rawDomain, err := url.PathUnescape(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid domain path"))
		return
	}

	domain := normalizeDomain(rawDomain)
	if domain == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid domain"))
		return
	}

	switch {
	case len(parts) == 1:
		s.domainDetail(w, r, domain)

	case len(parts) == 2 && parts[1] == "traffic":
		s.domainTraffic(w, r, domain)

	case len(parts) == 2 && parts[1] == "clients":
		s.domainClients(w, r, domain)

	default:
		http.NotFound(w, r)
	}
}

func (s *server) clientDetail(
	w http.ResponseWriter,
	r *http.Request,
	clientIP string,
) {
	rangeName, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var out clientDetailResponse
	out.Range = rangeName
	out.ClientIP = clientIP

	err = s.db.QueryRow(ctx, `
		SELECT
			sum(total_queries),
			sum(responses),
			sum(no_response),
			sum(unmatched_responses),
			sum(nxdomain),
			sum(servfail),
			sum(response_bytes),
			if(
				sum(latency_samples) = 0,
				0,
				sum(latency_us_sum) / sum(latency_samples)
			)
		FROM pulse.client_minute
		WHERE tenant_id = ?
		  AND client_ip = toIPv6(?)
		  AND minute >= ?
	`, s.tenant, clientIP, since).Scan(
		&out.Queries,
		&out.Responses,
		&out.NoResponse,
		&out.UnmatchedResponses,
		&out.NXDomain,
		&out.Servfail,
		&out.ResponseBytes,
		&out.AvgLatencyUS,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if out.Queries == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("client not found in selected range"))
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT maxMerge(last_seen_state)
		FROM pulse.client_last_seen
		WHERE tenant_id = ?
		  AND client_ip = toIPv6(?)
	`, s.tenant, clientIP).Scan(&out.LastSeen)
	if err != nil {
		writeDBError(w, err)
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT uniqCombined64Merge(domains_state)
		FROM pulse.client_unique_minute
		WHERE tenant_id = ?
		  AND client_ip = toIPv6(?)
		  AND minute >= ?
	`, s.tenant, clientIP, since).Scan(&out.UniqueDomains)
	if err != nil {
		writeDBError(w, err)
		return
	}

	var quantiles []float64
	if err := s.db.QueryRow(ctx, `SELECT quantilesTDigestMerge(0.5, 0.95, 0.99)(latency_state)
		FROM pulse.client_latency_minute WHERE tenant_id=? AND client_ip=toIPv6(?) AND minute>=?`, s.tenant, clientIP, since).Scan(&quantiles); err != nil {
		writeDBError(w, err)
		return
	}
	if len(quantiles) >= 3 {
		out.P50LatencyUS, out.P95LatencyUS, out.P99LatencyUS = finite(quantiles[0]), finite(quantiles[1]), finite(quantiles[2])
	}
	out.Sources, err = s.entitySources(ctx, "client_last_seen", "client_ip = toIPv6(?)", clientIP, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	out.State = entityState(out.LastSeen, time.Now().UTC())

	out.NoResponsePct, out.NXDomainPct, out.ServfailPct = transactionRates(out.Queries, out.Responses, out.NoResponse, out.NXDomain, out.Servfail)

	writeJSON(w, http.StatusOK, out)
}

func (s *server) domainDetail(
	w http.ResponseWriter,
	r *http.Request,
	domain string,
) {
	rangeName, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var out domainDetailResponse
	out.Range = rangeName
	out.Domain = domain

	err = s.db.QueryRow(ctx, `
		SELECT
			sum(total_queries),
			sum(responses),
			sum(no_response),
			sum(unmatched_responses),
			sum(nxdomain),
			sum(servfail),
			sum(response_bytes),
			if(
				sum(latency_samples) = 0,
				0,
				sum(latency_us_sum) / sum(latency_samples)
			)
		FROM pulse.domain_minute
		WHERE tenant_id = ?
		  AND qname = ?
		  AND minute >= ?
	`, s.tenant, domain, since).Scan(
		&out.Queries,
		&out.Responses,
		&out.NoResponse,
		&out.UnmatchedResponses,
		&out.NXDomain,
		&out.Servfail,
		&out.ResponseBytes,
		&out.AvgLatencyUS,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if out.Queries == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("domain not found in selected range"))
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT maxMerge(last_seen_state)
		FROM pulse.domain_last_seen
		WHERE tenant_id = ?
		  AND qname = ?
	`, s.tenant, domain).Scan(&out.LastSeen)
	if err != nil {
		writeDBError(w, err)
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT uniqCombined64Merge(clients_state)
		FROM pulse.domain_unique_minute
		WHERE tenant_id = ?
		  AND qname = ?
		  AND minute >= ?
	`, s.tenant, domain, since).Scan(&out.UniqueClients)
	if err != nil {
		writeDBError(w, err)
		return
	}

	var quantiles []float64
	if err := s.db.QueryRow(ctx, `SELECT quantilesTDigestMerge(0.5, 0.95, 0.99)(latency_state)
		FROM pulse.domain_latency_minute WHERE tenant_id=? AND qname=? AND minute>=?`, s.tenant, domain, since).Scan(&quantiles); err != nil {
		writeDBError(w, err)
		return
	}
	if len(quantiles) >= 3 {
		out.P50LatencyUS, out.P95LatencyUS, out.P99LatencyUS = finite(quantiles[0]), finite(quantiles[1]), finite(quantiles[2])
	}
	out.Sources, err = s.entitySources(ctx, "domain_last_seen", "qname = ?", domain, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	out.State = entityState(out.LastSeen, time.Now().UTC())

	rows, err := s.db.Query(ctx, `
		SELECT
			qtype,
			sum(total_queries)
		FROM pulse.domain_minute
		WHERE tenant_id = ?
		  AND qname = ?
		  AND minute >= ?
		GROUP BY qtype
		ORDER BY sum(total_queries) DESC
	`, s.tenant, domain, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	out.QTypes = make([]qtypeRow, 0)

	for rows.Next() {
		var row qtypeRow

		if err := rows.Scan(
			&row.QType,
			&row.Queries,
		); err != nil {
			writeDBError(w, err)
			return
		}

		out.QTypes = append(out.QTypes, row)
	}

	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	out.NoResponsePct, out.NXDomainPct, out.ServfailPct = transactionRates(out.Queries, out.Responses, out.NoResponse, out.NXDomain, out.Servfail)

	writeJSON(w, http.StatusOK, out)
}

func (s *server) clientTraffic(
	w http.ResponseWriter,
	r *http.Request,
	clientIP string,
) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)
	bucket := trafficBucket(duration)

	query := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			sum(total_queries),
			sum(responses),
			sum(no_response),
			sum(unmatched_responses),
			sum(nxdomain),
			sum(servfail),
			if(
				sum(latency_samples) = 0,
				0,
				sum(latency_us_sum) / sum(latency_samples)
			)
		FROM pulse.client_minute
		WHERE tenant_id = ?
		  AND client_ip = toIPv6(?)
		  AND minute >= ?
		GROUP BY bucket
		ORDER BY bucket
	`, bucket)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(
		ctx,
		query,
		s.tenant,
		clientIP,
		since,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	result := make([]trafficPoint, 0)

	for rows.Next() {
		var row trafficPoint

		if err := rows.Scan(
			&row.Time,
			&row.Queries,
			&row.Responses,
			&row.NoResponse,
			&row.UnmatchedResponses,
			&row.NXDomain,
			&row.Servfail,
			&row.AvgLatencyUS,
		); err != nil {
			writeDBError(w, err)
			return
		}

		result = append(result, row)
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *server) domainTraffic(
	w http.ResponseWriter,
	r *http.Request,
	domain string,
) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)
	bucket := trafficBucket(duration)

	query := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			sum(total_queries),
			sum(responses),
			sum(no_response),
			sum(unmatched_responses),
			sum(nxdomain),
			sum(servfail),
			if(
				sum(latency_samples) = 0,
				0,
				sum(latency_us_sum) / sum(latency_samples)
			)
		FROM pulse.domain_minute
		WHERE tenant_id = ?
		  AND qname = ?
		  AND minute >= ?
		GROUP BY bucket
		ORDER BY bucket
	`, bucket)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(
		ctx,
		query,
		s.tenant,
		domain,
		since,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	result := make([]trafficPoint, 0)

	for rows.Next() {
		var row trafficPoint

		if err := rows.Scan(
			&row.Time,
			&row.Queries,
			&row.Responses,
			&row.NoResponse,
			&row.UnmatchedResponses,
			&row.NXDomain,
			&row.Servfail,
			&row.AvgLatencyUS,
		); err != nil {
			writeDBError(w, err)
			return
		}

		result = append(result, row)
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *server) clientDomains(w http.ResponseWriter, r *http.Request, clientIP string) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if duration > 90*24*time.Hour {
		duration = 90 * 24 * time.Hour
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.db.Query(ctx, `SELECT qname, sum(total_queries), sum(responses), sum(no_response),
		sum(nxdomain), sum(servfail), sum(response_bytes)
		FROM pulse.client_domain_hour
		WHERE tenant_id=? AND client_ip=toIPv6(?) AND hour>=?
		GROUP BY qname ORDER BY sum(total_queries) DESC LIMIT ?`, s.tenant, clientIP, time.Now().UTC().Add(-duration), parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	result := make([]investigationDomainRow, 0)
	for rows.Next() {
		var row investigationDomainRow
		if err := rows.Scan(&row.Domain, &row.Queries, &row.Responses, &row.NoResponse, &row.NXDomain, &row.Servfail, &row.ResponseBytes); err != nil {
			writeDBError(w, err)
			return
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) domainClients(w http.ResponseWriter, r *http.Request, domain string) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if duration > 90*24*time.Hour {
		duration = 90 * 24 * time.Hour
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := s.db.Query(ctx, `SELECT toString(client_ip), sum(total_queries), sum(responses), sum(no_response),
		sum(nxdomain), sum(servfail), sum(response_bytes)
		FROM pulse.client_domain_hour
		WHERE tenant_id=? AND qname=? AND hour>=?
		GROUP BY client_ip ORDER BY sum(total_queries) DESC LIMIT ?`, s.tenant, domain, time.Now().UTC().Add(-duration), parseLimit(r))
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()
	result := make([]investigationClientRow, 0)
	for rows.Next() {
		var row investigationClientRow
		if err := rows.Scan(&row.ClientIP, &row.Queries, &row.Responses, &row.NoResponse, &row.NXDomain, &row.Servfail, &row.ResponseBytes); err != nil {
			writeDBError(w, err)
			return
		}
		row.ClientIP = normalizeIP(row.ClientIP)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) entitySources(ctx context.Context, table, predicate string, value any, since time.Time) ([]entitySource, error) {
	query := fmt.Sprintf(`SELECT source_id, maxMerge(last_seen_state) AS last_seen
		FROM pulse.%s WHERE tenant_id=? AND %s
		GROUP BY source_id HAVING last_seen>=? ORDER BY last_seen DESC`, table, predicate)
	rows, err := s.db.Query(ctx, query, s.tenant, value, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]entitySource, 0)
	for rows.Next() {
		var item entitySource
		if err := rows.Scan(&item.SourceID, &item.LastSeen); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func entityState(lastSeen, now time.Time) string {
	if now.Sub(lastSeen) <= 60*time.Second {
		return "active"
	}
	return "silent"
}
