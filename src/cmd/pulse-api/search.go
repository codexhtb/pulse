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
	Count      int           `json:"count"`
	Events     []searchEvent `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type searchCursor struct {
	EventTime  time.Time `json:"t"`
	IngestedAt time.Time `json:"i"`
	DNSID      uint16    `json:"d"`
	ClientPort uint16    `json:"p"`
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	rangeName, duration, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// RAW retention is currently 7 days.
	if duration > 7*24*time.Hour {
		writeError(
			w,
			http.StatusBadRequest,
			fmt.Errorf("raw event search currently supports a maximum range of 7d"),
		)
		return
	}

	limit := parseSearchLimit(r)

	now := time.Now().UTC()
	since := now.Add(-duration)

	where := []string{
		"tenant_id = ?",
		"event_time >= ?",
	}

	args := []any{
		s.tenant,
		since,
	}

	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source != "" {
		where = append(where, "source_id = ?")
		args = append(args, source)
	}

	domain := normalizeDomain(r.URL.Query().Get("domain"))
	if domain != "" {
		where = append(where, "qname = ?")
		args = append(args, domain)
	}

	clientIP := strings.TrimSpace(r.URL.Query().Get("client_ip"))
	if clientIP != "" {
		ip := net.ParseIP(clientIP)
		if ip == nil {
			writeError(
				w,
				http.StatusBadRequest,
				fmt.Errorf("invalid client_ip %q", clientIP),
			)
			return
		}

		where = append(where, "client_ip = toIPv6(?)")
		args = append(args, ip.String())
	}

	qtype := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("qtype")))
	if qtype != "" {
		where = append(where, "qtype = ?")
		args = append(args, qtype)
	}

	rcode := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("rcode")))
	if rcode != "" {
		where = append(where, "rcode = ?")
		args = append(args, rcode)
	}

	outcome := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("outcome")))
	if outcome != "" {
		if outcome != outcomeResponse && outcome != outcomeNoResponse && outcome != outcomeUnmatchedResponse {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid outcome"))
			return
		}
		where = append(where, "outcome = ?")
		args = append(args, outcome)
	}
	if parseBoolQuery(r.URL.Query().Get("failures")) {
		where = append(where, "(outcome = 'NO_RESPONSE' OR rcode IN ('SERVFAIL', 'REFUSED', 'FORMERR'))")
	}
	if value := strings.TrimSpace(r.URL.Query().Get("slow_us")); value != "" {
		slowUS, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("slow_us must be a non-negative integer"))
			return
		}
		where = append(where, "outcome = 'RESPONSE' AND latency_us >= ?")
		args = append(args, uint32(slowUS))
	}

	protocol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("protocol")))
	if protocol != "" {
		if protocol != "UDP" && protocol != "TCP" {
			writeError(
				w,
				http.StatusBadRequest,
				fmt.Errorf("protocol must be UDP or TCP"),
			)
			return
		}

		where = append(where, "protocol = ?")
		args = append(args, protocol)
	}

	cursorValue := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if cursorValue != "" {
		cursor, err := decodeSearchCursor(cursorValue)
		if err != nil {
			writeError(
				w,
				http.StatusBadRequest,
				fmt.Errorf("invalid cursor"),
			)
			return
		}

		where = append(
			where,
			`(event_time, ingested_at, dns_id, client_port) <
			(
				toDateTime64(?, 6, 'UTC'),
				toDateTime64(?, 6, 'UTC'),
				?,
				?
			)`,
		)

		args = append(
			args,
			cursor.EventTime.UTC().Format("2006-01-02 15:04:05.000000"),
			cursor.IngestedAt.UTC().Format("2006-01-02 15:04:05.000000"),
			cursor.DNSID,
			cursor.ClientPort,
		)
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
	`,
		strings.Join(where, "\n AND "),
		limit,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		writeDBError(w, err)
		return
	}
	defer rows.Close()

	events := make([]searchEvent, 0, limit)

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
		Range:  rangeName,
		Count:  len(events),
		Events: events,
	}

	if len(events) == limit {
		last := events[len(events)-1]

		cursor, err := encodeSearchCursor(searchCursor{
			EventTime:  last.EventTime,
			IngestedAt: last.IngestedAt,
			DNSID:      last.DNSID,
			ClientPort: last.ClientPort,
		})
		if err == nil {
			response.NextCursor = cursor
		}
	}

	writeJSON(w, http.StatusOK, response)
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
