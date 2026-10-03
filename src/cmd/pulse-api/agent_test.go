package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeControlIP(t *testing.T) {
	for input, want := range map[string]string{
		"192.0.2.10":        "192.0.2.10",
		"::ffff:192.0.2.10": "192.0.2.10",
	} {
		got, err := normalizeControlIP(input)
		if err != nil || got != want {
			t.Fatalf("normalizeControlIP(%q)=%q error=%v, want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"invalid", "192.0.2.0/24", "2001:db8::1"} {
		if _, err := normalizeControlIP(input); err == nil {
			t.Fatalf("normalizeControlIP(%q) accepted", input)
		}
	}
}

type testAgent struct {
	server      *httptest.Server
	token       string
	unavailable atomic.Bool
	requests    atomic.Int64
	mu          sync.Mutex
	actions     []string
}

func newTestAgent(t *testing.T, token string) *testAgent {
	t.Helper()
	agent := &testAgent{token: token}
	agent.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent.requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+agent.token {
			http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
			return
		}
		if agent.unavailable.Load() {
			http.Error(w, `{"error":"temporarily unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok","mode":"dry-run"}`)
		case "/block":
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			agent.mu.Lock()
			agent.actions = append(agent.actions, "block:"+request["ip"])
			agent.mu.Unlock()
			_, _ = io.WriteString(w, `{"ip":"`+request["ip"]+`","status":"applied","result":"state applied"}`)
		case "/unblock":
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			agent.mu.Lock()
			agent.actions = append(agent.actions, "unblock:"+request["ip"])
			agent.mu.Unlock()
			_, _ = io.WriteString(w, `{"status":"applied"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(agent.server.Close)
	return agent
}

func configuredTestNode(id, identity, token string, agent *testAgent, enabled bool) *controlNode {
	return &controlNode{
		config: controlNodeConfig{
			ID: id, DisplayName: id, SourceIdentity: identity, AgentURL: agent.server.URL,
			TokenFile: "/not-used/" + id, DNSServiceIP: "192.0.2.53", ControlEnabled: enabled,
		},
		client: &agentClient{baseURL: agent.server.URL, token: token, client: agent.server.Client()},
	}
}

func nodeStatus(t *testing.T, view dnsControlView, nodeID string) controlNodeView {
	t.Helper()
	for _, node := range view.Nodes {
		if node.ID == nodeID {
			return node
		}
	}
	t.Fatalf("node %q not found in %+v", nodeID, view.Nodes)
	return controlNodeView{}
}

func TestControlPlanePartialFailureDurableRecoveryAndDisabled(t *testing.T) {
	tokenA := strings.Repeat("a", 32)
	tokenB := strings.Repeat("b", 32)
	tokenDisabled := strings.Repeat("c", 32)
	agentA := newTestAgent(t, tokenA)
	agentB := newTestAgent(t, tokenB)
	disabledAgent := newTestAgent(t, tokenDisabled)
	agentB.unavailable.Store(true)
	nodes := []*controlNode{
		configuredTestNode("node-a", "dns-a", tokenA, agentA, true),
		configuredTestNode("node-b", "dns-b", tokenB, agentB, true),
		configuredTestNode("observer", "dns-observer", tokenDisabled, disabledAgent, false),
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	statePath := filepath.Join(t.TempDir(), "control.json")
	plane, err := newControlPlane(nodes, newControlStateStore(statePath), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := plane.setBlock(context.Background(), "192.0.2.10", dnsBlockRequest{Duration: "1h", Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "partial" || nodeStatus(t, view, "node-a").Status != applyApplied || nodeStatus(t, view, "node-b").Status != applyError {
		t.Fatalf("unexpected partial view: %+v", view)
	}
	if got := nodeStatus(t, view, "observer").Status; got != applyDisabled {
		t.Fatalf("disabled status=%q", got)
	}
	if disabledAgent.requests.Load() != 0 {
		t.Fatalf("disabled node received %d requests", disabledAgent.requests.Load())
	}
	plane.refreshHealth(context.Background())
	if disabledAgent.requests.Load() != 0 {
		t.Fatalf("disabled node received %d requests during health refresh", disabledAgent.requests.Load())
	}
	if health := nodeStatus(t, plane.get("192.0.2.10"), "observer").Health; health.Status != applyDisabled {
		t.Fatalf("disabled node health=%+v, want status %q", health, applyDisabled)
	}
	loaded, err := newControlStateStore(statePath).load()
	if err != nil || loaded.Records["192.0.2.10"].Nodes["node-b"].Status != applyError {
		t.Fatalf("partial result was not durable: state=%+v err=%v", loaded, err)
	}

	agentB.unavailable.Store(false)
	now = now.Add(3 * time.Second)
	restarted, err := newControlPlane(nodes, newControlStateStore(statePath), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.reconcileAll(context.Background())
	view = restarted.get("192.0.2.10")
	if view.Status != applyApplied || nodeStatus(t, view, "node-b").Status != applyApplied {
		t.Fatalf("recovery did not converge: %+v", view)
	}

	view, err = restarted.setUnblock(context.Background(), "192.0.2.10")
	if err != nil || view.Desired != desiredUnblocked || view.Status != applyApplied {
		t.Fatalf("unblock view=%+v err=%v", view, err)
	}
	reloaded, err := newControlPlane(nodes, newControlStateStore(statePath), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.get("192.0.2.10"); got.Desired != desiredUnblocked || got.Status != applyApplied {
		t.Fatalf("unblock did not survive restart: %+v", got)
	}
}

func TestControlPlaneBackoffAvoidsTightRetryLoop(t *testing.T) {
	token := strings.Repeat("d", 32)
	agent := newTestAgent(t, token)
	agent.unavailable.Store(true)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	plane, err := newControlPlane(
		[]*controlNode{configuredTestNode("node", "dns", token, agent, true)},
		newControlStateStore(filepath.Join(t.TempDir(), "control.json")),
		func() time.Time { return now }, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	view, err := plane.setBlock(context.Background(), "192.0.2.11", dnsBlockRequest{Duration: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	first := agent.requests.Load()
	result := nodeStatus(t, view, "node")
	if result.NextRetryAt == nil || !result.NextRetryAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("next_retry_at=%v", result.NextRetryAt)
	}
	plane.reconcileAll(context.Background())
	if got := agent.requests.Load(); got != first {
		t.Fatalf("tight retry occurred: requests %d -> %d", first, got)
	}
	now = now.Add(2 * time.Second)
	plane.reconcileAll(context.Background())
	if got := agent.requests.Load(); got != first+1 {
		t.Fatalf("scheduled retry missing: requests=%d", got)
	}
}

func TestControlPlaneExpiryProducesDurableUnblock(t *testing.T) {
	token := strings.Repeat("e", 32)
	agent := newTestAgent(t, token)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "control.json")
	plane, err := newControlPlane(
		[]*controlNode{configuredTestNode("node", "dns", token, agent, true)},
		newControlStateStore(path), func() time.Time { return now }, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plane.setBlock(context.Background(), "192.0.2.12", dnsBlockRequest{Duration: "1h"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour + time.Second)
	plane.reconcileAll(context.Background())
	view := plane.get("192.0.2.12")
	if view.Desired != desiredUnblocked || view.Status != applyApplied {
		t.Fatalf("expiry did not converge to unblock: %+v", view)
	}
	state, err := newControlStateStore(path).load()
	if err != nil || state.Records["192.0.2.12"].Desired != desiredUnblocked {
		t.Fatalf("expiry transition not durable: %+v err=%v", state, err)
	}
}

func TestLoadControlNodesRejectsSharedSecrets(t *testing.T) {
	directory := t.TempDir()
	tokenA := filepath.Join(directory, "a.token")
	tokenB := filepath.Join(directory, "b.token")
	if err := os.WriteFile(tokenA, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenB, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "nodes.json")
	config := `{"nodes":[` +
		`{"id":"one","display_name":"One","source_identity":"dns-one","agent_url":"http://127.0.0.1:9001","token_file":"` + tokenA + `","dns_service_ip":"192.0.2.1","control_enabled":true},` +
		`{"id":"two","display_name":"Two","source_identity":"dns-two","agent_url":"http://127.0.0.1:9002","token_file":"` + tokenB + `","dns_service_ip":"192.0.2.2","control_enabled":true}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadControlNodes(configPath); err == nil || !strings.Contains(err.Error(), "same bearer secret") {
		t.Fatalf("shared secret was accepted: %v", err)
	}
}

func TestDNSBlockRouteReturnsPerNodeState(t *testing.T) {
	token := strings.Repeat("f", 32)
	agent := newTestAgent(t, token)
	plane, err := newControlPlane(
		[]*controlNode{configuredTestNode("resolver-a", "resolver-a", token, agent, true)},
		newControlStateStore(filepath.Join(t.TempDir(), "control.json")), time.Now, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	server := &server{control: plane}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clients/::ffff:192.0.2.10/dns-block", strings.NewReader(`{"duration":"1h","reason":"test"}`))
	recorder := httptest.NewRecorder()
	server.clientRoute(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var view dnsControlView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.IP != "192.0.2.10" || len(view.Nodes) != 1 || view.Nodes[0].Status != applyApplied {
		t.Fatalf("unexpected view: %+v", view)
	}
}
