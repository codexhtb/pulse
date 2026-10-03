package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type server struct {
	db                      clickhouse.Conn
	tenant                  string
	live                    *liveHub
	origins                 map[string]struct{}
	startedAt               time.Time
	metrics                 apiRuntimeMetrics
	collectorStatusURL      string
	httpClient              *http.Client
	systemStorage           func(context.Context) (storageStatus, []persistedSource, error)
	storagePath             string
	storageWarningPct       float64
	storageCriticalPct      float64
	clickhouseSystemMetrics bool
	control                 *controlPlane
	auth                    *authManager
}

type overviewResponse struct {
	Range              string           `json:"range"`
	CurrentQPS         float64          `json:"current_qps"`
	Queries            uint64           `json:"queries"`
	Responses          uint64           `json:"responses"`
	NoResponse         uint64           `json:"no_response"`
	NoResponsePct      float64          `json:"no_response_pct"`
	UnmatchedResponses uint64           `json:"unmatched_responses"`
	QueriesToday       uint64           `json:"queries_today"`
	UniqueClients      uint64           `json:"unique_clients"`
	UniqueDomains      uint64           `json:"unique_domains"`
	NXDomain           uint64           `json:"nxdomain"`
	NXDomainPct        float64          `json:"nxdomain_pct"`
	Servfail           uint64           `json:"servfail"`
	ServfailPct        float64          `json:"servfail_pct"`
	AvgLatencyUS       float64          `json:"avg_latency_us"`
	P50LatencyUS       float64          `json:"p50_latency_us"`
	P95LatencyUS       float64          `json:"p95_latency_us"`
	P99LatencyUS       float64          `json:"p99_latency_us"`
	ResponseBytes      uint64           `json:"response_bytes"`
	ActiveSources      uint64           `json:"active_sources"`
	Previous           overviewPrevious `json:"previous"`
}

type overviewPrevious struct {
	CurrentQPS   float64 `json:"current_qps"`
	Queries      uint64  `json:"queries"`
	NXDomainPct  float64 `json:"nxdomain_pct"`
	ServfailPct  float64 `json:"servfail_pct"`
	P95LatencyUS float64 `json:"p95_latency_us"`
}

type trafficPoint struct {
	Time               time.Time `json:"time"`
	Queries            uint64    `json:"queries"`
	Responses          uint64    `json:"responses"`
	NoResponse         uint64    `json:"no_response"`
	UnmatchedResponses uint64    `json:"unmatched_responses"`
	NXDomain           uint64    `json:"nxdomain"`
	Servfail           uint64    `json:"servfail"`
	AvgLatencyUS       float64   `json:"avg_latency_us"`
	P50LatencyUS       float64   `json:"p50_latency_us"`
	P95LatencyUS       float64   `json:"p95_latency_us"`
	LatencySamples     uint64    `json:"latency_samples"`
	QPS                float64   `json:"qps"`
	BucketSeconds      uint32    `json:"bucket_seconds"`
	NXDomainPct        float64   `json:"nxdomain_pct"`
	ServfailPct        float64   `json:"servfail_pct"`
}

type domainRow struct {
	Domain   string `json:"domain"`
	Queries  uint64 `json:"queries"`
	NXDomain uint64 `json:"nxdomain"`
	Servfail uint64 `json:"servfail"`
}

type clientRow struct {
	ClientIP    string  `json:"client_ip"`
	Queries     uint64  `json:"queries"`
	Responses   uint64  `json:"responses"`
	NoResponse  uint64  `json:"no_response"`
	NXDomain    uint64  `json:"nxdomain"`
	Servfail    uint64  `json:"servfail"`
	NXDomainPct float64 `json:"nxdomain_pct"`
}

type rcodeRow struct {
	RCode   string `json:"rcode"`
	Queries uint64 `json:"queries"`
}

type sourceRow struct {
	SourceID         string     `json:"source_id"`
	Queries          uint64     `json:"queries"`
	Responses        uint64     `json:"responses"`
	NXDomain         uint64     `json:"nxdomain"`
	NXDomainPct      float64    `json:"nxdomain_pct"`
	Servfail         uint64     `json:"servfail"`
	ServfailPct      float64    `json:"servfail_pct"`
	CurrentQPS       float64    `json:"current_qps"`
	P95LatencyUS     float64    `json:"p95_latency_us"`
	LastSeen         *time.Time `json:"last_seen"`
	LastMessageAt    *time.Time `json:"last_message_at"`
	LastPersistedAt  *time.Time `json:"last_persisted_at"`
	AgeSeconds       *int64     `json:"age_seconds"`
	IngestLagSeconds *float64   `json:"ingest_lag_seconds"`
	Expected         bool       `json:"expected"`
	Connected        bool       `json:"connected"`
	State            string     `json:"state"`
	Health           string     `json:"health"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8081", "HTTP listen address")
	authStatePath := flag.String("auth-state", getenv("PULSE_AUTH_STATE_FILE", "/var/lib/pulse-api/auth.json"), "authentication state path")
	bootstrapAdmin := flag.String("bootstrap-admin", "", "create the first Admin using a password read from stdin, then exit")
	flag.Parse()

	if *bootstrapAdmin != "" {
		if err := bootstrapAdminFromStdin(*authStatePath, *bootstrapAdmin, os.Stdin); err != nil {
			log.Fatalf("bootstrap Admin: %v", err)
		}
		log.Printf("Admin %q created", *bootstrapAdmin)
		return
	}

	host := getenv("CLICKHOUSE_HOST", "127.0.0.1")
	port := getenv("CLICKHOUSE_PORT", "9000")
	database := getenv("CLICKHOUSE_DATABASE", "pulse")
	user := getenv("CLICKHOUSE_USER", "pulse_api")
	password := os.Getenv("CLICKHOUSE_PASSWORD")
	tenant := getenv("PULSE_TENANT", "default")

	if password == "" {
		log.Fatal("CLICKHOUSE_PASSWORD is empty")
	}

	db, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{net.JoinHostPort(host, port)},
		Auth: clickhouse.Auth{
			Database: database,
			Username: user,
			Password: password,
		},
		DialTimeout:  5 * time.Second,
		MaxOpenConns: 8,
		MaxIdleConns: 4,
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
	})
	if err != nil {
		log.Fatalf("open ClickHouse: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		log.Fatalf("ClickHouse ping: %v", err)
	}

	liveListen := getenv(
		"PULSE_LIVE_UDP_LISTEN",
		"127.0.0.1:9092",
	)

	live, err := newLiveHub(liveListen)
	if err != nil {
		log.Fatalf("initialize live hub: %v", err)
	}

	s := &server{
		db:                      db,
		tenant:                  tenant,
		live:                    live,
		startedAt:               time.Now().UTC(),
		collectorStatusURL:      getenv("PULSE_COLLECTOR_STATUS_URL", "http://127.0.0.1:9093/internal/status"),
		httpClient:              &http.Client{Timeout: 3 * time.Second},
		storagePath:             strings.TrimSpace(os.Getenv("PULSE_STORAGE_PATH")),
		storageWarningPct:       getenvFloat("PULSE_STORAGE_WARNING_PCT", 70),
		storageCriticalPct:      getenvFloat("PULSE_STORAGE_CRITICAL_PCT", 85),
		clickhouseSystemMetrics: parseBoolQuery(os.Getenv("PULSE_CLICKHOUSE_SYSTEM_METRICS")),
		origins: map[string]struct{}{
			"http://127.0.0.1:5173": {},
			"http://localhost:5173": {},
		},
	}
	auth, err := newAuthManager(*authStatePath, time.Now)
	if err != nil {
		log.Fatalf("initialize authentication: %v", err)
	}
	s.auth = auth
	runContext, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	controlConfigPath := strings.TrimSpace(os.Getenv("PULSE_CONTROL_NODES_CONFIG"))
	if controlConfigPath != "" {
		nodes, err := loadControlNodes(controlConfigPath)
		if err != nil {
			log.Fatalf("load control nodes: %v", err)
		}
		statePath := getenv("PULSE_CONTROL_STATE_FILE", "/var/lib/pulse-api/dns-control.json")
		control, err := newControlPlane(nodes, newControlStateStore(statePath), time.Now, log.Default())
		if err != nil {
			log.Fatalf("initialize DNS control plane: %v", err)
		}
		s.control = control
		s.control.start(runContext)
	}

	apiMux := http.NewServeMux()

	apiMux.HandleFunc("/api/v1/health", s.health)
	apiMux.HandleFunc("/api/v1/meta", s.meta)
	apiMux.HandleFunc("/api/v1/anomalies", s.anomalies)
	apiMux.HandleFunc("/api/v1/system", s.system)
	apiMux.HandleFunc("/api/v1/overview", s.overview)
	apiMux.HandleFunc("/api/v1/traffic", s.traffic)
	apiMux.HandleFunc("/api/v1/domains/top", s.topDomains)
	apiMux.HandleFunc("/api/v1/clients/top", s.topClients)
	apiMux.HandleFunc("/api/v1/rcodes", s.rcodes)
	apiMux.HandleFunc("/api/v1/sources", s.sources)
	apiMux.HandleFunc("/api/v1/sources/", s.sourceRoute)
	apiMux.HandleFunc("/api/v1/search", s.search)
	apiMux.HandleFunc("/api/v1/live", s.liveStream)
	apiMux.HandleFunc("/api/v1/clients/", s.clientRoute)
	apiMux.HandleFunc("/api/v1/domains/", s.domainRoute)
	apiMux.HandleFunc("/api/v1/dns-blocks", s.dnsBlocks)
	apiMux.HandleFunc("/api/v1/control/nodes", s.controlNodes)
	apiMux.HandleFunc("/api/v1/admin/users", s.adminUsers)
	apiMux.HandleFunc("/api/v1/admin/users/", s.adminUserRoute)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", s.authLogin)
	mux.HandleFunc("/api/v1/auth/session", s.authSession)
	mux.HandleFunc("/api/v1/auth/logout", s.authLogout)
	// Container-local health checks need a real dependency probe without an
	// authenticated browser session. This port is not published by the Docker
	// deployment and nginx does not proxy /internal paths.
	mux.HandleFunc("/internal/health", s.health)
	mux.Handle("/api/", s.requireAuthentication(apiMux))

	handler := s.middleware(mux)

	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf(
		"pulse-api started listen=%s tenant=%s clickhouse=%s",
		*listen,
		tenant,
		net.JoinHostPort(host, port),
	)

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)
	go func() {
		<-shutdownSignals
		log.Printf("shutdown requested")
		stopBackground()
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelShutdown()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			log.Printf("graceful HTTP shutdown: %v", err)
		}
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Printf("shutdown complete")
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"error":  "clickhouse unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "pulse-api",
		"time":    time.Now().UTC(),
	})
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	name, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	now := time.Now().UTC()
	since := now.Add(-duration)
	previousSince := since.Add(-duration)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var out overviewResponse
	out.Range = name

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
			),
			uniqExact(source_id)
		FROM pulse.traffic_minute
		WHERE tenant_id = ?
		  AND minute >= ?
	`, s.tenant, since).Scan(
		&out.Queries,
		&out.Responses,
		&out.NoResponse,
		&out.UnmatchedResponses,
		&out.NXDomain,
		&out.Servfail,
		&out.ResponseBytes,
		&out.AvgLatencyUS,
		&out.ActiveSources,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}

	var previousResponses uint64
	var previousNXDomain uint64
	var previousServfail uint64
	err = s.db.QueryRow(ctx, `
		SELECT
			sum(total_queries),
			sum(responses),
			sum(nxdomain),
			sum(servfail)
		FROM pulse.traffic_minute
		WHERE tenant_id = ?
		  AND minute >= ?
		  AND minute < ?
	`, s.tenant, previousSince, since).Scan(
		&out.Previous.Queries,
		&previousResponses,
		&previousNXDomain,
		&previousServfail,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT uniqExact(client_ip)
		FROM pulse.client_minute
		WHERE tenant_id = ?
		  AND minute >= ?
	`, s.tenant, since).Scan(&out.UniqueClients)
	if err != nil {
		writeDBError(w, err)
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT uniqExact(qname)
		FROM pulse.domain_minute
		WHERE tenant_id = ?
		  AND minute >= ?
	`, s.tenant, since).Scan(&out.UniqueDomains)
	if err != nil {
		writeDBError(w, err)
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT sum(total_queries)
		FROM pulse.traffic_minute
		WHERE tenant_id = ?
		  AND minute >= toStartOfDay(now())
	`, s.tenant).Scan(&out.QueriesToday)
	if err != nil {
		writeDBError(w, err)
		return
	}

	err = s.db.QueryRow(ctx, `
		SELECT
			countIf(outcome IN ('RESPONSE', 'NO_RESPONSE') AND event_time >= ? AND event_time < ?) / 60.0,
			countIf(outcome IN ('RESPONSE', 'NO_RESPONSE') AND event_time >= ? AND event_time < ?) / 60.0
		FROM pulse.dns_events
		WHERE tenant_id = ?
		  AND event_time >= ?
		  AND event_time < ?
	`, now.Add(-time.Minute), now, now.Add(-2*time.Minute), now.Add(-time.Minute), s.tenant, now.Add(-2*time.Minute), now).Scan(&out.CurrentQPS, &out.Previous.CurrentQPS)
	if err != nil {
		writeDBError(w, err)
		return
	}

	var quantiles []float64

	err = s.db.QueryRow(ctx, `
		SELECT
			quantilesTDigestMerge(0.5, 0.95, 0.99)(latency_state)
		FROM pulse.latency_minute
		WHERE tenant_id = ?
		  AND minute >= ?
	`, s.tenant, since).Scan(&quantiles)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if len(quantiles) >= 3 {
		out.P50LatencyUS = quantiles[0]
		out.P95LatencyUS = quantiles[1]
		out.P99LatencyUS = quantiles[2]
	}

	var previousQuantiles []float64
	err = s.db.QueryRow(ctx, `
		SELECT quantilesTDigestMerge(0.5, 0.95, 0.99)(latency_state)
		FROM pulse.latency_minute
		WHERE tenant_id = ?
		  AND minute >= ?
		  AND minute < ?
	`, s.tenant, previousSince, since).Scan(&previousQuantiles)
	if err != nil {
		writeDBError(w, err)
		return
	}
	if len(previousQuantiles) >= 2 {
		out.Previous.P95LatencyUS = previousQuantiles[1]
	}

	out.NoResponsePct, out.NXDomainPct, out.ServfailPct = transactionRates(out.Queries, out.Responses, out.NoResponse, out.NXDomain, out.Servfail)
	_, out.Previous.NXDomainPct, out.Previous.ServfailPct = transactionRates(out.Previous.Queries, previousResponses, 0, previousNXDomain, previousServfail)

	out.sanitizeFiniteMetrics()

	writeJSON(w, http.StatusOK, out)
}

func transactionRates(totalQueries, responses, noResponse, nxdomain, servfail uint64) (float64, float64, float64) {
	var noResponseRate, nxdomainRate, servfailRate float64
	if totalQueries > 0 {
		noResponseRate = 100 * float64(noResponse) / float64(totalQueries)
	}
	if responses > 0 {
		nxdomainRate = 100 * float64(nxdomain) / float64(responses)
		servfailRate = 100 * float64(servfail) / float64(responses)
	}
	return noResponseRate, nxdomainRate, servfailRate
}

func (out *overviewResponse) sanitizeFiniteMetrics() {
	metrics := []*float64{
		&out.CurrentQPS,
		&out.NXDomainPct,
		&out.NoResponsePct,
		&out.ServfailPct,
		&out.AvgLatencyUS,
		&out.P50LatencyUS,
		&out.P95LatencyUS,
		&out.P99LatencyUS,
		&out.Previous.CurrentQPS,
		&out.Previous.NXDomainPct,
		&out.Previous.ServfailPct,
		&out.Previous.P95LatencyUS,
	}

	for _, metric := range metrics {
		if math.IsNaN(*metric) || math.IsInf(*metric, 0) {
			*metric = 0
		}
	}
}

func (s *server) traffic(w http.ResponseWriter, r *http.Request) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)
	bucket, bucketSeconds := trafficBucketSpec(duration)

	query := fmt.Sprintf(`
		SELECT
			t.bucket,
			t.queries,
			t.responses,
			t.no_response,
			t.unmatched_responses,
			t.nxdomain,
			t.servfail,
			t.avg_latency_us,
			toFloat64(if(length(l.quantiles) >= 1, l.quantiles[1], 0)),
			toFloat64(if(length(l.quantiles) >= 2, l.quantiles[2], 0)),
			t.latency_sample_count,
			t.queries / toFloat64(%d),
			toUInt32(%d),
			if(t.responses = 0, 0, 100 * t.nxdomain / t.responses),
			if(t.responses = 0, 0, 100 * t.servfail / t.responses)
		FROM
		(
			SELECT
				%s AS bucket,
				sum(total_queries) AS queries,
				sum(responses) AS responses,
				sum(no_response) AS no_response,
				sum(unmatched_responses) AS unmatched_responses,
				sum(nxdomain) AS nxdomain,
				sum(servfail) AS servfail,
				if(sum(latency_samples) = 0, 0, sum(latency_us_sum) / sum(latency_samples)) AS avg_latency_us,
				sum(latency_samples) AS latency_sample_count
			FROM pulse.traffic_minute
			WHERE tenant_id = ? AND minute >= ?
			GROUP BY bucket
		) AS t
		LEFT JOIN
		(
			SELECT
				%s AS bucket,
				quantilesTDigestMerge(0.5, 0.95)(latency_state) AS quantiles
			FROM pulse.latency_minute
			WHERE tenant_id = ? AND minute >= ?
			GROUP BY bucket
		) AS l USING bucket
		ORDER BY t.bucket
	`, bucketSeconds, bucketSeconds, bucket, bucket)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, query, s.tenant, since, s.tenant, since)
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
			&row.P50LatencyUS,
			&row.P95LatencyUS,
			&row.LatencySamples,
			&row.QPS,
			&row.BucketSeconds,
			&row.NXDomainPct,
			&row.ServfailPct,
		); err != nil {
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

func (s *server) topDomains(w http.ResponseWriter, r *http.Request) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	limit := parseLimit(r)
	since := time.Now().UTC().Add(-duration)

	query := fmt.Sprintf(`
		SELECT
			qname,
			sum(total_queries),
			sum(nxdomain),
			sum(servfail)
		FROM pulse.domain_minute
		WHERE tenant_id = ?
		  AND minute >= ?
		GROUP BY qname
		ORDER BY sum(total_queries) DESC
		LIMIT %d
	`, limit)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, query, s.tenant, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	result := make([]domainRow, 0, limit)

	for rows.Next() {
		var row domainRow

		if err := rows.Scan(
			&row.Domain,
			&row.Queries,
			&row.NXDomain,
			&row.Servfail,
		); err != nil {
			writeDBError(w, err)
			return
		}

		result = append(result, row)
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *server) topClients(w http.ResponseWriter, r *http.Request) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	limit := parseLimit(r)
	since := time.Now().UTC().Add(-duration)

	query := fmt.Sprintf(`
		SELECT
			toString(client_ip),
			sum(total_queries),
			sum(responses),
			sum(no_response),
			sum(nxdomain),
			sum(servfail)
		FROM pulse.client_minute
		WHERE tenant_id = ?
		  AND minute >= ?
		GROUP BY client_ip
		ORDER BY sum(total_queries) DESC
		LIMIT %d
	`, limit)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, query, s.tenant, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	result := make([]clientRow, 0, limit)

	for rows.Next() {
		var row clientRow

		if err := rows.Scan(
			&row.ClientIP,
			&row.Queries,
			&row.Responses,
			&row.NoResponse,
			&row.NXDomain,
			&row.Servfail,
		); err != nil {
			writeDBError(w, err)
			return
		}

		row.ClientIP = normalizeIP(row.ClientIP)

		if row.Responses > 0 {
			row.NXDomainPct =
				100 * float64(row.NXDomain) / float64(row.Responses)
		}

		result = append(result, row)
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *server) rcodes(w http.ResponseWriter, r *http.Request) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, `
		SELECT
			rcode,
			sum(responses)
		FROM pulse.rcode_minute
		WHERE tenant_id = ?
		  AND minute >= ?
		GROUP BY rcode
		ORDER BY sum(responses) DESC
	`, s.tenant, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	result := make([]rcodeRow, 0)

	for rows.Next() {
		var row rcodeRow

		if err := rows.Scan(&row.RCode, &row.Queries); err != nil {
			writeDBError(w, err)
			return
		}

		result = append(result, row)
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *server) sources(w http.ResponseWriter, r *http.Request) {
	_, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Now().UTC().Add(-duration)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, `
		SELECT
			t.source_id,
			t.queries,
			t.responses,
			t.nxdomain,
			if(t.responses = 0, 0, 100 * t.nxdomain / t.responses),
			t.servfail,
			if(t.responses = 0, 0, 100 * t.servfail / t.responses),
			toFloat64(if(length(l.quantiles) >= 2, l.quantiles[2], 0))
		FROM
		(
			SELECT source_id, sum(total_queries) AS queries, sum(responses) AS responses, sum(nxdomain) AS nxdomain, sum(servfail) AS servfail
			FROM pulse.traffic_minute
			WHERE tenant_id = ? AND minute >= ?
			GROUP BY source_id
		) AS t
		LEFT JOIN
		(
			SELECT source_id, quantilesTDigestMerge(0.5, 0.95, 0.99)(latency_state) AS quantiles
			FROM pulse.latency_minute
			WHERE tenant_id = ? AND minute >= ?
			GROUP BY source_id
		) AS l USING source_id
		ORDER BY t.source_id
	`, s.tenant, since, s.tenant, since)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	byID := make(map[string]*sourceRow)

	for rows.Next() {
		var row sourceRow

		if err := rows.Scan(
			&row.SourceID,
			&row.Queries,
			&row.Responses,
			&row.NXDomain,
			&row.NXDomainPct,
			&row.Servfail,
			&row.ServfailPct,
			&row.P95LatencyUS,
		); err != nil {
			writeDBError(w, err)
			return
		}
		copy := row
		byID[row.SourceID] = &copy
	}
	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	qpsRows, err := s.db.Query(ctx, `
		SELECT source_id, countIf(outcome IN ('RESPONSE', 'NO_RESPONSE')) / 60.0
		FROM pulse.dns_events
		WHERE tenant_id = ? AND event_time >= now64(6) - INTERVAL 60 SECOND
		GROUP BY source_id
	`, s.tenant)
	if err != nil {
		writeDBError(w, err)
		return
	}
	for qpsRows.Next() {
		var sourceID string
		var qps float64
		if err := qpsRows.Scan(&sourceID, &qps); err != nil {
			qpsRows.Close()
			writeDBError(w, err)
			return
		}
		row := byID[sourceID]
		if row == nil {
			row = &sourceRow{SourceID: sourceID}
			byID[sourceID] = row
		}
		row.CurrentQPS = qps
	}
	if err := qpsRows.Close(); err != nil {
		writeDBError(w, err)
		return
	}

	persistedRows, err := s.db.Query(ctx, `
		SELECT source_id, maxMerge(last_seen_state)
		FROM pulse.source_last_seen
		WHERE tenant_id = ?
		GROUP BY source_id
	`, s.tenant)
	if err != nil {
		writeDBError(w, err)
		return
	}
	persisted := make([]persistedSource, 0)
	for persistedRows.Next() {
		var item persistedSource
		if err := persistedRows.Scan(&item.SourceID, &item.LastSeen); err != nil {
			persistedRows.Close()
			writeDBError(w, err)
			return
		}
		persisted = append(persisted, item)
		row := byID[item.SourceID]
		if row == nil {
			row = &sourceRow{SourceID: item.SourceID}
			byID[item.SourceID] = row
		}
		lastSeen := item.LastSeen
		row.LastSeen = &lastSeen
	}
	if err := persistedRows.Close(); err != nil {
		writeDBError(w, err)
		return
	}

	collector, err := s.fetchCollectorStatus(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "collector status unavailable"})
		return
	}
	now := time.Now().UTC()
	merged := mergeSystemSources(now, time.Duration(collector.CorrelationTimeoutMS)*time.Millisecond, collector.Sources, persisted)
	result := make([]sourceRow, 0, len(merged))
	for _, source := range merged {
		row := byID[source.SourceID]
		if row == nil {
			row = &sourceRow{SourceID: source.SourceID}
		}
		row.Expected = source.Expected
		row.Connected = source.Connected
		row.State = source.State
		row.Health = resolverHealth(source)
		row.LastMessageAt = source.LastMessageAt
		row.LastPersistedAt = source.LastPersistedAt
		row.AgeSeconds = source.AgeSeconds
		if row.LastSeen == nil && source.LastPersistedAt != nil {
			lastSeen := *source.LastPersistedAt
			row.LastSeen = &lastSeen
		}
		if source.LastPersistedAt != nil {
			lag := now.Sub(*source.LastPersistedAt).Seconds()
			if lag < 0 {
				lag = 0
			}
			row.IngestLagSeconds = &lag
		}
		result = append(result, *row)
	}

	writeJSON(w, http.StatusOK, result)
}

func resolverHealth(source systemSource) string {
	if source.State == "active" && source.Expected {
		return "healthy"
	}
	if source.State == "active" || source.State == "connected_no_events" || source.State == "silent" {
		return "degraded"
	}
	return "offline"
}

func parseRange(r *http.Request) (string, time.Duration, error) {
	value := r.URL.Query().Get("range")

	if value == "" {
		value = "1h"
	}

	ranges := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  time.Hour,
		"3h":  3 * time.Hour,
		"6h":  6 * time.Hour,
		"12h": 12 * time.Hour,
		"24h": 24 * time.Hour,
	}

	duration, ok := ranges[value]
	if !ok {
		return "", 0, fmt.Errorf(
			"invalid range %q; allowed: 15m,1h,3h,6h,12h,24h",
			value,
		)
	}

	return value, duration, nil
}

func trafficBucket(duration time.Duration) string {
	bucket, _ := trafficBucketSpec(duration)
	return bucket
}

func trafficBucketSpec(duration time.Duration) (string, uint32) {
	switch {
	case duration <= 6*time.Hour:
		return "toStartOfMinute(minute)", 60

	default:
		return "toStartOfInterval(minute, INTERVAL 5 MINUTE)", 300
	}
}

func parseLimit(r *http.Request) int {
	value := r.URL.Query().Get("limit")

	if value == "" {
		return 10
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return 10
	}

	if n < 1 {
		return 1
	}

	if n > 100 {
		return 100
	}

	return n
}

func normalizeIP(value string) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return value
	}

	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}

	return ip.String()
}

func (s *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		origin := r.Header.Get("Origin")
		if _, ok := s.origins[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, X-CSRF-Token",
			)
			w.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, PATCH, DELETE, OPTIONS",
			)
		}

		if r.Method == http.MethodOptions {
			recorder.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(recorder, r)
		durationUS := uint64(time.Since(start).Microseconds())
		s.metrics.requests.Add(1)
		s.metrics.totalLatencyUS.Add(durationUS)
		updateMax(&s.metrics.maxLatencyUS, durationUS)
		if recorder.status >= 400 {
			s.metrics.errors.Add(1)
			s.metrics.lastErrorNS.Store(time.Now().UTC().UnixNano())
		}

		log.Printf(
			"http method=%s path=%s remote=%s duration=%s",
			r.Method,
			r.URL.Path,
			r.RemoteAddr,
			time.Since(start),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
func (r *statusRecorder) Write(data []byte) (int, error) { return r.ResponseWriter.Write(data) }
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func writeDBError(w http.ResponseWriter, err error) {
	log.Printf("database error: %v", err)

	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "database query failed",
	})
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{
		"error": err.Error(),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode JSON: %v", err)
	}
}

func getenv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func getenvFloat(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
