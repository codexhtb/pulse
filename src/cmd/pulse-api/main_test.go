package main

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOverviewResponseSanitizesNonFiniteMetrics(t *testing.T) {
	out := overviewResponse{
		Range:         "1h",
		CurrentQPS:    math.NaN(),
		NXDomainPct:   math.Inf(1),
		ServfailPct:   math.Inf(-1),
		AvgLatencyUS:  math.NaN(),
		P50LatencyUS:  math.NaN(),
		P95LatencyUS:  math.Inf(1),
		P99LatencyUS:  math.Inf(-1),
		Queries:       42,
		ResponseBytes: 128,
		UniqueClients: 3,
		UniqueDomains: 7,
		ActiveSources: 1,
	}

	out.sanitizeFiniteMetrics()

	_, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("overview response is not valid JSON: %v", err)
	}

	for name, value := range map[string]float64{
		"current_qps":    out.CurrentQPS,
		"nxdomain_pct":   out.NXDomainPct,
		"servfail_pct":   out.ServfailPct,
		"avg_latency_us": out.AvgLatencyUS,
		"p50_latency_us": out.P50LatencyUS,
		"p95_latency_us": out.P95LatencyUS,
		"p99_latency_us": out.P99LatencyUS,
	} {
		if value != 0 {
			t.Errorf("%s = %v, want 0", name, value)
		}
	}

	if out.Queries != 42 || out.ResponseBytes != 128 {
		t.Fatalf("finite counters changed: %+v", out)
	}
}

func TestOperationalBucketDurations(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		wantSQL  string
		wantSec  uint32
	}{
		{15 * time.Minute, "toStartOfMinute(minute)", 60},
		{6 * time.Hour, "toStartOfMinute(minute)", 60},
		{12 * time.Hour, "toStartOfInterval(minute, INTERVAL 5 MINUTE)", 300},
		{24 * time.Hour, "toStartOfInterval(minute, INTERVAL 5 MINUTE)", 300},
	} {
		gotSQL, gotSec := trafficBucketSpec(test.duration)
		if gotSQL != test.wantSQL || gotSec != test.wantSec {
			t.Fatalf("duration=%s bucket=(%q,%d), want=(%q,%d)", test.duration, gotSQL, gotSec, test.wantSQL, test.wantSec)
		}
	}
}

func TestResolverHealthUsesRuntimeState(t *testing.T) {
	for _, test := range []struct {
		source systemSource
		want   string
	}{
		{systemSource{State: "active", Expected: true}, "healthy"},
		{systemSource{State: "active", Unexpected: true}, "degraded"},
		{systemSource{State: "silent", Expected: true}, "degraded"},
		{systemSource{State: "disconnected", Expected: true}, "offline"},
		{systemSource{State: "never_seen_since_start", Expected: true}, "offline"},
	} {
		if got := resolverHealth(test.source); got != test.want {
			t.Fatalf("source=%+v health=%q want=%q", test.source, got, test.want)
		}
	}
}

func TestLiveInvestigationFilters(t *testing.T) {
	latency := int64(750000)
	response := liveEvent{Outcome: outcomeResponse, RCode: "NOERROR", LatencyUS: &latency}
	servfail := liveEvent{Outcome: outcomeResponse, RCode: "SERVFAIL", LatencyUS: &latency}
	nxdomain := liveEvent{Outcome: outcomeResponse, RCode: "NXDOMAIN", LatencyUS: &latency}
	noResponse := liveEvent{Outcome: outcomeNoResponse}

	if !matchLiveFilter(noResponse, liveFilter{Outcome: outcomeNoResponse}) || matchLiveFilter(response, liveFilter{Outcome: outcomeNoResponse}) {
		t.Fatal("outcome filter mismatch")
	}
	if !matchLiveFilter(servfail, liveFilter{FailuresOnly: true}) || !matchLiveFilter(noResponse, liveFilter{FailuresOnly: true}) || matchLiveFilter(nxdomain, liveFilter{FailuresOnly: true}) {
		t.Fatal("failure semantics mismatch")
	}
	if !matchLiveFilter(response, liveFilter{SlowUS: 500000}) || matchLiveFilter(response, liveFilter{SlowUS: 800000}) || matchLiveFilter(noResponse, liveFilter{SlowUS: 1}) {
		t.Fatal("slow filter mismatch")
	}

	request := httptest.NewRequest("GET", "/api/v1/live?outcome=NO_RESPONSE&failures=true&slow_us=250000", nil)
	filter, err := parseLiveFilter(request, "default")
	if err != nil || filter.Outcome != outcomeNoResponse || !filter.FailuresOnly || filter.SlowUS != 250000 {
		t.Fatalf("parsed filter=%+v err=%v", filter, err)
	}
}

func TestSearchCursorRoundTripUnchanged(t *testing.T) {
	want := searchCursor{
		EventTime:  time.Date(2026, 9, 30, 9, 1, 2, 345000000, time.UTC),
		IngestedAt: time.Date(2026, 9, 30, 9, 1, 3, 456000000, time.UTC),
		DNSID:      65530,
		ClientPort: 53000,
	}
	encoded, err := encodeSearchCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeSearchCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cursor=%+v want=%+v", got, want)
	}
}
