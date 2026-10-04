package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newSearchRequest(values url.Values) *http.Request {
	return httptest.NewRequest("GET", "/api/v1/search?"+values.Encode(), nil)
}

func TestCustomSearchWindowUsesHalfOpenBoundsAndTimezone(t *testing.T) {
	request := newSearchRequest(url.Values{
		"range": {"custom"},
		"from":  {"2026-10-04T10:27:00+03:00"},
		"to":    {"2026-10-04T10:29:00+03:00"},
	})
	window, err := parseSearchWindow(request, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	wantFrom := time.Date(2026, 10, 4, 7, 27, 0, 0, time.UTC)
	wantTo := time.Date(2026, 10, 4, 7, 29, 0, 0, time.UTC)
	if window.From != wantFrom || window.To != wantTo {
		t.Fatalf("window=%s..%s want=%s..%s", window.From, window.To, wantFrom, wantTo)
	}

	for _, test := range []struct {
		name      string
		timestamp time.Time
		want      bool
	}{
		{"start included", wantFrom, true},
		{"last microsecond included", wantTo.Add(-time.Microsecond), true},
		{"end excluded", wantTo, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := window.contains(test.timestamp); got != test.want {
				t.Fatalf("contains(%s)=%t want=%t", test.timestamp, got, test.want)
			}
		})
	}
}

func TestCustomSearchWindowValidation(t *testing.T) {
	tests := []struct {
		name   string
		values url.Values
	}{
		{"missing to", url.Values{"range": {"custom"}, "from": {"2026-10-04T10:27:00+03:00"}}},
		{"equal", url.Values{"from": {"2026-10-04T10:27:00+03:00"}, "to": {"2026-10-04T10:27:00+03:00"}}},
		{"reversed", url.Values{"from": {"2026-10-04T10:29:00+03:00"}, "to": {"2026-10-04T10:27:00+03:00"}}},
		{"missing timezone", url.Values{"from": {"2026-10-04T10:27:00"}, "to": {"2026-10-04T10:29:00"}}},
		{"relative conflict", url.Values{"range": {"1h"}, "from": {"2026-10-04T10:27:00+03:00"}, "to": {"2026-10-04T10:29:00+03:00"}}},
		{"over retention", url.Values{"from": {"2026-10-03T10:27:00+03:00"}, "to": {"2026-10-04T10:29:00+03:00"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseSearchWindow(newSearchRequest(test.values), time.Time{}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCustomSearchValidationReturnsBadRequest(t *testing.T) {
	request := newSearchRequest(url.Values{
		"range": {"custom"},
		"from":  {"2026-10-04T10:29:00+03:00"},
		"to":    {"2026-10-04T10:29:00+03:00"},
	})
	recorder := httptest.NewRecorder()
	(&server{tenant: "default"}).search(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestCustomSearchPlanCombinesTimeAndEventFilters(t *testing.T) {
	request := newSearchRequest(url.Values{
		"range":   {"custom"},
		"from":    {"2026-10-04T10:27:00+03:00"},
		"to":      {"2026-10-04T10:29:00+03:00"},
		"source":  {"dns1"},
		"rcode":   {"servfail"},
		"outcome": {"response"},
	})
	plan, err := buildSearchPlan(request, "default", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	where := strings.Join(plan.Where, " ")
	for _, predicate := range []string{
		"event_time >= toDateTime64(?, 6, 'UTC')",
		"event_time < toDateTime64(?, 6, 'UTC')",
		"source_id = ?",
		"rcode = ?",
		"outcome = ?",
	} {
		if !strings.Contains(where, predicate) {
			t.Errorf("missing predicate %q in %q", predicate, where)
		}
	}
	wantArgs := []any{
		"default",
		"2026-10-04 07:27:00.000000",
		"2026-10-04 07:29:00.000000",
		"dns1",
		"SERVFAIL",
		"RESPONSE",
	}
	if !reflect.DeepEqual(plan.Args, wantArgs) {
		t.Fatalf("args=%#v want=%#v", plan.Args, wantArgs)
	}
}

func TestCustomSearchCursorKeepsWindowOnPageTwo(t *testing.T) {
	from := time.Date(2026, 10, 4, 7, 27, 0, 0, time.UTC)
	to := time.Date(2026, 10, 4, 7, 29, 0, 0, time.UTC)
	cursorValue, err := encodeSearchCursor(searchCursor{
		EventTime:  time.Date(2026, 10, 4, 7, 28, 0, 0, time.UTC),
		IngestedAt: time.Date(2026, 10, 4, 7, 28, 0, 123000, time.UTC),
		DNSID:      100,
		ClientPort: 53000,
		WindowFrom: &from,
		WindowTo:   &to,
	})
	if err != nil {
		t.Fatal(err)
	}

	values := url.Values{
		"range":  {"custom"},
		"from":   {"2026-10-04T10:27:00+03:00"},
		"to":     {"2026-10-04T10:29:00+03:00"},
		"cursor": {cursorValue},
	}
	plan, err := buildSearchPlan(newSearchRequest(values), "default", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	where := strings.Join(plan.Where, " ")
	if !strings.Contains(where, "event_time < toDateTime64(?, 6, 'UTC')") ||
		!strings.Contains(where, "(event_time, ingested_at, dns_id, client_port) <") {
		t.Fatalf("page-two predicates do not preserve window and tuple cursor: %s", where)
	}

	values.Set("to", "2026-10-04T10:30:00+03:00")
	if _, err := buildSearchPlan(newSearchRequest(values), "default", time.Time{}); err == nil {
		t.Fatal("cursor was accepted with a different custom window")
	}
}

func TestSearchCursorDistinguishesEqualEventTimestamps(t *testing.T) {
	eventTime := time.Date(2026, 10, 4, 7, 28, 0, 0, time.UTC)
	first, err := encodeSearchCursor(searchCursor{
		EventTime: eventTime, IngestedAt: eventTime.Add(time.Microsecond), DNSID: 100, ClientPort: 53000,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeSearchCursor(searchCursor{
		EventTime: eventTime, IngestedAt: eventTime.Add(2 * time.Microsecond), DNSID: 101, ClientPort: 53001,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("tuple cursors collapsed distinct events with the same event_time")
	}
}

func TestMetaReportsRawRetentionUsedBySearch(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&server{}).meta(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	var response struct {
		Retention map[string]string `json:"retention"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Retention["raw"] != rawEventRetentionLabel {
		t.Fatalf("raw retention=%q want=%q", response.Retention["raw"], rawEventRetentionLabel)
	}
}

func TestRelativeSearchPlanRemainsBackwardCompatible(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	plan, err := buildSearchPlan(newSearchRequest(url.Values{"range": {"1h"}}), "default", now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Window.Custom || plan.Window.Range != "1h" || plan.Window.From != now.Add(-time.Hour) {
		t.Fatalf("unexpected relative window: %+v", plan.Window)
	}
	if strings.Contains(strings.Join(plan.Where, " "), "event_time <") {
		t.Fatal("relative search semantics unexpectedly gained an upper bound")
	}
}
