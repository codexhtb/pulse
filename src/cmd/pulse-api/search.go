package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type searchEvent struct {
	EventTime     time.Time  `json:"event_time"`
	IngestedAt    time.Time  `json:"ingested_at"`
	QueryTime     *time.Time `json:"query_time"`
	ResponseTime  *time.Time `json:"response_time"`
	Outcome       string     `json:"outcome"`
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
	LatencyUS     *uint32    `json:"latency_us"`
	MatchedQuery  bool       `json:"matched_query"`
}

type searchResponse struct {
	Range      string        `json:"range"`
	From       *time.Time    `json:"from,omitempty"`
	To         *time.Time    `json:"to,omitempty"`
	Count      int           `json:"count"`
	Events     []searchEvent `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type searchCursor struct {
	EventTime  time.Time  `json:"t"`
	IngestedAt time.Time  `json:"i"`
	DNSID      uint16     `json:"d"`
	ClientPort uint16     `json:"p"`
	WindowFrom *time.Time `json:"f,omitempty"`
	WindowTo   *time.Time `json:"e,omitempty"`
}

const (
	rawEventRetention      = 24 * time.Hour
	rawEventRetentionLabel = "24h"
)

type searchTimeWindow struct {
	Range  string
	From   time.Time
	To     time.Time
	Custom bool
}

func (window searchTimeWindow) contains(timestamp time.Time) bool {
	timestamp = timestamp.UTC()
	if timestamp.Before(window.From) {
		return false
	}
	return !window.Custom || timestamp.Before(window.To)
}

type searchPlan struct {
	Window searchTimeWindow
	Where  []string
	Args   []any
	Limit  int
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	plan, err := buildSearchPlan(r, s.tenant, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	query := fmt.Sprintf(`
		SELECT
			event_time,
			ingested_at,
			query_time,
			response_time,
			outcome,
			source_id,
			toString(client_ip),
			client_port,
			protocol,
			qname,
			qtype,
			qclass,
			rcode,
			dns_id,
			response_bytes,
			answer_count,
			ifNull(toInt64(latency_us), -1),
			matched_query
		FROM pulse.dns_events
		WHERE %s
		ORDER BY
			event_time DESC,
			ingested_at DESC,
			dns_id DESC,
			client_port DESC
		LIMIT %d
	`, strings.Join(plan.Where, "\n AND "), plan.Limit)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, query, plan.Args...)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	events := make([]searchEvent, 0, plan.Limit)

	for rows.Next() {
		var event searchEvent
		var latency int64
		var matched uint8

		if err := rows.Scan(
			&event.EventTime,
			&event.IngestedAt,
			&event.QueryTime,
			&event.ResponseTime,
			&event.Outcome,
			&event.SourceID,
			&event.ClientIP,
			&event.ClientPort,
			&event.Protocol,
			&event.QName,
			&event.QType,
			&event.QClass,
			&event.RCode,
			&event.DNSID,
			&event.ResponseBytes,
			&event.AnswerCount,
			&latency,
			&matched,
		); err != nil {
			writeDBError(w, err)
			return
		}

		event.ClientIP = normalizeIP(event.ClientIP)
		event.MatchedQuery = matched != 0

		if latency >= 0 {
			value := uint32(latency)
			event.LatencyUS = &value
		}

		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	response := searchResponse{
		Range:  plan.Window.Range,
		Count:  len(events),
		Events: events,
	}
	if plan.Window.Custom {
		from, to := plan.Window.From, plan.Window.To
		response.From, response.To = &from, &to
	}

	if len(events) == plan.Limit {
		last := events[len(events)-1]
		cursor := searchCursor{
			EventTime:  last.EventTime,
			IngestedAt: last.IngestedAt,
			DNSID:      last.DNSID,
			ClientPort: last.ClientPort,
		}
		if plan.Window.Custom {
			from, to := plan.Window.From, plan.Window.To
			cursor.WindowFrom, cursor.WindowTo = &from, &to
		}

		encoded, err := encodeSearchCursor(cursor)
		if err == nil {
			response.NextCursor = encoded
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func buildSearchPlan(r *http.Request, tenant string, now time.Time) (searchPlan, error) {
	window, err := parseSearchWindow(r, now)
	if err != nil {
		return searchPlan{}, err
	}

	plan := searchPlan{
		Window: window,
		Where:  []string{"tenant_id = ?"},
		Args:   []any{tenant},
		Limit:  parseSearchLimit(r),
	}
	if window.Custom {
		plan.Where = append(plan.Where,
			"event_time >= toDateTime64(?, 6, 'UTC')",
			"event_time < toDateTime64(?, 6, 'UTC')",
		)
		plan.Args = append(plan.Args, formatClickHouseTime(window.From), formatClickHouseTime(window.To))
	} else {
		plan.Where = append(plan.Where, "event_time >= ?")
		plan.Args = append(plan.Args, window.From)
	}

	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source != "" {
		plan.Where = append(plan.Where, "source_id = ?")
		plan.Args = append(plan.Args, source)
	}

	domain := normalizeDomain(r.URL.Query().Get("domain"))
	if domain != "" {
		plan.Where = append(plan.Where, "qname = ?")
		plan.Args = append(plan.Args, domain)
	}

	clientIP := strings.TrimSpace(r.URL.Query().Get("client_ip"))
	if clientIP != "" {
		ip := net.ParseIP(clientIP)
		if ip == nil {
			return searchPlan{}, fmt.Errorf("invalid client_ip %q", clientIP)
		}

		plan.Where = append(plan.Where, "client_ip = toIPv6(?)")
		plan.Args = append(plan.Args, ip.String())
	}

	qtype := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("qtype")))
	if qtype != "" {
		plan.Where = append(plan.Where, "qtype = ?")
		plan.Args = append(plan.Args, qtype)
	}

	rcode := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("rcode")))
	if rcode != "" {
		plan.Where = append(plan.Where, "rcode = ?")
		plan.Args = append(plan.Args, rcode)
	}

	outcome := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("outcome")))
	if outcome != "" {
		if outcome != outcomeResponse && outcome != outcomeNoResponse && outcome != outcomeUnmatchedResponse {
			return searchPlan{}, fmt.Errorf("invalid outcome")
		}
		plan.Where = append(plan.Where, "outcome = ?")
		plan.Args = append(plan.Args, outcome)
	}
	if parseBoolQuery(r.URL.Query().Get("failures")) {
		plan.Where = append(plan.Where, "(outcome = 'NO_RESPONSE' OR rcode IN ('SERVFAIL', 'REFUSED', 'FORMERR'))")
	}
	if value := strings.TrimSpace(r.URL.Query().Get("slow_us")); value != "" {
		slowUS, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return searchPlan{}, fmt.Errorf("slow_us must be a non-negative integer")
		}
		plan.Where = append(plan.Where, "outcome = 'RESPONSE' AND latency_us >= ?")
		plan.Args = append(plan.Args, uint32(slowUS))
	}

	protocol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("protocol")))
	if protocol != "" {
		if protocol != "UDP" && protocol != "TCP" {
			return searchPlan{}, fmt.Errorf("protocol must be UDP or TCP")
		}

		plan.Where = append(plan.Where, "protocol = ?")
		plan.Args = append(plan.Args, protocol)
	}

	cursorValue := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if cursorValue != "" {
		cursor, err := decodeSearchCursor(cursorValue)
		if err != nil {
			return searchPlan{}, fmt.Errorf("invalid cursor")
		}
		if window.Custom {
			if cursor.WindowFrom == nil || cursor.WindowTo == nil ||
				!cursor.WindowFrom.Equal(window.From) || !cursor.WindowTo.Equal(window.To) {
				return searchPlan{}, fmt.Errorf("cursor does not match the selected custom time window")
			}
		} else if cursor.WindowFrom != nil || cursor.WindowTo != nil {
			return searchPlan{}, fmt.Errorf("cursor requires its original custom time window")
		}

		plan.Where = append(
			plan.Where,
			`(event_time, ingested_at, dns_id, client_port) <
			(
				toDateTime64(?, 6, 'UTC'),
				toDateTime64(?, 6, 'UTC'),
				?,
				?
			)`,
		)

		plan.Args = append(
			plan.Args,
			cursor.EventTime.UTC().Format("2006-01-02 15:04:05.000000"),
			cursor.IngestedAt.UTC().Format("2006-01-02 15:04:05.000000"),
			cursor.DNSID,
			cursor.ClientPort,
		)
	}

	return plan, nil
}

func parseSearchWindow(r *http.Request, now time.Time) (searchTimeWindow, error) {
	query := r.URL.Query()
	fromValue := strings.TrimSpace(query.Get("from"))
	toValue := strings.TrimSpace(query.Get("to"))
	rangeValue := strings.TrimSpace(query.Get("range"))

	if fromValue != "" || toValue != "" || rangeValue == "custom" {
		if fromValue == "" || toValue == "" {
			return searchTimeWindow{}, fmt.Errorf("custom search requires both from and to RFC3339 timestamps")
		}
		if rangeValue != "" && rangeValue != "custom" {
			return searchTimeWindow{}, fmt.Errorf("relative range cannot be combined with from and to")
		}
		from, err := parseSearchTimestamp("from", fromValue)
		if err != nil {
			return searchTimeWindow{}, err
		}
		to, err := parseSearchTimestamp("to", toValue)
		if err != nil {
			return searchTimeWindow{}, err
		}
		if !from.Before(to) {
			return searchTimeWindow{}, fmt.Errorf("from must be earlier than to")
		}
		if to.Sub(from) > rawEventRetention {
			return searchTimeWindow{}, fmt.Errorf("custom search window cannot exceed %s", rawEventRetentionLabel)
		}
		from, to = ceilToMicrosecond(from), ceilToMicrosecond(to)
		if !from.Before(to) {
			return searchTimeWindow{}, fmt.Errorf("custom search window is smaller than ClickHouse timestamp precision")
		}
		return searchTimeWindow{Range: "custom", From: from, To: to, Custom: true}, nil
	}

	rangeName, duration, err := parseRange(r)
	if err != nil {
		return searchTimeWindow{}, err
	}
	return searchTimeWindow{Range: rangeName, From: now.UTC().Add(-duration)}, nil
}

func parseSearchTimestamp(name, value string) (time.Time, error) {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp with timezone offset", name)
	}
	return timestamp.UTC(), nil
}

func ceilToMicrosecond(timestamp time.Time) time.Time {
	timestamp = timestamp.UTC()
	truncated := timestamp.Truncate(time.Microsecond)
	if truncated.Before(timestamp) {
		return truncated.Add(time.Microsecond)
	}
	return truncated
}

func formatClickHouseTime(timestamp time.Time) string {
	return timestamp.UTC().Format("2006-01-02 15:04:05.000000")
}

func parseSearchLimit(r *http.Request) int {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 100
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return 100
	}

	if n < 1 {
		return 1
	}

	if n > 500 {
		return 500
	}

	return n
}

func normalizeDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	return value
}

func encodeSearchCursor(cursor searchCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeSearchCursor(value string) (searchCursor, error) {
	var cursor searchCursor

	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, err
	}

	if err := json.Unmarshal(data, &cursor); err != nil {
		return cursor, err
	}

	return cursor, nil
}
