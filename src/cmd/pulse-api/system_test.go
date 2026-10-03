package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTransactionRateDenominators(t *testing.T) {
	noResponse, nx, sf := transactionRates(100, 80, 20, 8, 4)
	if noResponse != 20 || nx != 10 || sf != 5 {
		t.Fatalf("rates no_response=%v nxdomain=%v servfail=%v", noResponse, nx, sf)
	}
}

func TestSystemEndpoint(t *testing.T) {
	now := time.Now().UTC()
	collectorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(collectorRuntimeStatus{
			ProcessStartedAt: now.Add(-time.Minute), UptimeSeconds: 60, CorrelationTimeoutMS: 30000,
			CountersSinceStart: map[string]uint64{"frames_received": 10}, ActiveConnections: 1,
			Persistence:        collectorPersistenceStatus{QueueCapacity: 100000, InsertedTotal: 10},
			PendingPersistence: collectorPendingPersistenceStatus{Enabled: true},
			Live:               collectorLiveStatus{QueueCapacity: 50000, SentTotal: 10},
			Sources:            []collectorSourceStatus{{Identity: "dns1", RemoteAddress: "192.0.2.53:1234", ActiveConnections: 1, LastMessageAt: now, LastPersistedAt: now}},
			Time:               now,
		})
	}))
	defer collectorServer.Close()
	s := &server{
		tenant: "default", startedAt: now.Add(-2 * time.Minute), live: &liveHub{subs: make(map[uint64]*liveSubscriber)},
		collectorStatusURL: collectorServer.URL, httpClient: collectorServer.Client(),
		systemStorage: func(context.Context) (storageStatus, []persistedSource, error) {
			return storageStatus{RawRows: 10}, []persistedSource{{SourceID: "dns1", LastSeen: now}}, nil
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system", nil)
	s.system(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response systemResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "healthy" || response.Collector.CorrelationTimeoutMS != 30000 || response.Storage.RawRows != 10 || len(response.Sources) != 1 || response.Sources[0].State != "active" {
		t.Fatalf("response=%+v", response)
	}
}

func TestSystemJSONSanitizesNonFiniteValues(t *testing.T) {
	if finite(math.NaN()) != 0 || finite(math.Inf(1)) != 0 {
		t.Fatal("non-finite values were not sanitized")
	}
	response := systemResponse{Status: "healthy", API: apiStatus{RequestLatency: map[string]float64{"average": finite(math.NaN())}}, Time: time.Now().UTC()}
	if _, err := json.Marshal(response); err != nil {
		t.Fatalf("system response JSON: %v", err)
	}
}

func TestDurableLossIsCriticalButLiveLossOnlyDegraded(t *testing.T) {
	now := time.Now().UTC()
	sources := []systemSource{{SourceID: "dns1", State: "active"}}
	collector := collectorRuntimeStatus{Persistence: collectorPersistenceStatus{QueueCapacity: 100, DurableDropped: 1}, PendingPersistence: collectorPendingPersistenceStatus{Enabled: true}}
	status, _ := evaluateSystemStatus(now, collector, apiLiveStatus{}, storageStatus{}, sources)
	if status != "critical" {
		t.Fatalf("durable status=%s", status)
	}
	collector.Persistence.DurableDropped = 0
	collector.Live.Dropped = 1
	status, sub := evaluateSystemStatus(now, collector, apiLiveStatus{}, storageStatus{}, sources)
	if status != "degraded" || sub["durable"] != "healthy" || sub["live"] != "degraded" {
		t.Fatalf("status=%s subs=%v", status, sub)
	}
}

func TestExpectedSourceMissingAndUnexpectedSource(t *testing.T) {
	now := time.Now().UTC()
	collector := collectorRuntimeStatus{Persistence: collectorPersistenceStatus{QueueCapacity: 100}, PendingPersistence: collectorPendingPersistenceStatus{Enabled: true}}
	sources := []systemSource{
		{SourceID: "dns1", Expected: true, State: "active"},
		{SourceID: "dns2", Expected: true, State: "never_seen_since_start"},
	}
	status, sub := evaluateSystemStatus(now, collector, apiLiveStatus{}, storageStatus{}, sources)
	if status != "degraded" || sub["sources"] != "degraded" {
		t.Fatalf("one missing expected source status=%s subs=%v", status, sub)
	}
	sources = []systemSource{{SourceID: "dns1", Expected: true, State: "active"}, {SourceID: "rogue", Unexpected: true, State: "active"}}
	status, sub = evaluateSystemStatus(now, collector, apiLiveStatus{}, storageStatus{}, sources)
	if status != "degraded" || sub["sources"] != "degraded" {
		t.Fatalf("unexpected source status=%s subs=%v", status, sub)
	}
}

func TestStorageThresholds(t *testing.T) {
	now := time.Now().UTC()
	collector := collectorRuntimeStatus{Persistence: collectorPersistenceStatus{QueueCapacity: 100}, PendingPersistence: collectorPendingPersistenceStatus{Enabled: true}}
	sources := []systemSource{{SourceID: "dns1", Expected: true, State: "active"}}
	status, sub := evaluateSystemStatus(now, collector, apiLiveStatus{}, storageStatus{FilesystemStatus: "warning"}, sources)
	if status != "degraded" || sub["storage"] != "degraded" {
		t.Fatalf("warning storage status=%s subs=%v", status, sub)
	}
	status, sub = evaluateSystemStatus(now, collector, apiLiveStatus{}, storageStatus{FilesystemStatus: "critical"}, sources)
	if status != "critical" || sub["storage"] != "critical" {
		t.Fatalf("critical storage status=%s subs=%v", status, sub)
	}
}

func TestAttachControlNodesUsesSourceIdentityWithoutInventingTelemetrySource(t *testing.T) {
	sources := []systemSource{{SourceID: "resolver-a", State: "active"}}
	nodes := []controlNodeView{
		{ID: "node-a", SourceIdentity: "resolver-a", ControlEnabled: true},
		{ID: "mock", SourceIdentity: "mock-without-telemetry", ControlEnabled: true},
	}
	got := attachControlNodes(sources, nodes)
	if len(got) != 1 {
		t.Fatalf("control-only node created a telemetry source: %+v", got)
	}
	if got[0].Control == nil || got[0].Control.ID != "node-a" {
		t.Fatalf("control node was not joined by source_identity: %+v", got[0])
	}
}

func TestEvaluateControlStatusIgnoresDisabledNodes(t *testing.T) {
	nodes := []controlNodeView{
		{ID: "enabled", ControlEnabled: true, Health: nodeHealthState{Status: "ok"}},
		{ID: "observer", ControlEnabled: false, Health: nodeHealthState{Status: "unavailable"}},
	}
	if got := evaluateControlStatus(nodes); got != "healthy" {
		t.Fatalf("status=%q, want healthy", got)
	}
	nodes = append(nodes, controlNodeView{ID: "down", ControlEnabled: true, Health: nodeHealthState{Status: "unavailable"}})
	if got := evaluateControlStatus(nodes); got != "degraded" {
		t.Fatalf("status=%q, want degraded", got)
	}
}
