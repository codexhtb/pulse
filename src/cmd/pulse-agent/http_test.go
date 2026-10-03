package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestHTTPBlockListUnblock(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	agent := newAgent(log.New(io.Discard, "", 0), func() time.Time { return now })
	server := httptest.NewServer(agent.handler())
	defer server.Close()

	requestJSON(t, http.MethodPost, server.URL+"/block", `{"ip":"::ffff:192.0.2.44","ttl":"1h"}`, http.StatusOK)
	response := requestJSON(t, http.MethodGet, server.URL+"/list", "", http.StatusOK)
	var listed struct {
		Count  int           `json:"count"`
		Blocks []blockRecord `json:"blocks"`
	}
	if err := json.Unmarshal(response, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Count != 1 || listed.Blocks[0].IP != "192.0.2.44" {
		t.Fatalf("unexpected list response: %s", response)
	}

	requestJSON(t, http.MethodPost, server.URL+"/unblock", `{"ip":"192.0.2.44"}`, http.StatusOK)
	response = requestJSON(t, http.MethodGet, server.URL+"/list", "", http.StatusOK)
	if err := json.Unmarshal(response, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Count != 0 {
		t.Fatalf("unexpected list response after unblock: %s", response)
	}
}

func TestHTTPHealthReportsDryRun(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	agent := newAgent(log.New(io.Discard, "", 0), func() time.Time { return now })
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	agent.handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var health map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" || health["mode"] != "enforcing" || health["service"] != "pulse-agent" {
		t.Fatalf("unexpected health response: %s", recorder.Body.String())
	}
}

func TestAuthenticationRejectsMissingTokenAndWrongCaller(t *testing.T) {
	agent := newAgent(log.New(io.Discard, "", 0), time.Now)
	handler := authenticated(agent.handler(), strings.Repeat("a", 32), []netip.Addr{netip.MustParseAddr("192.0.2.10")})

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.RemoteAddr = "192.0.2.10:53000"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/health", nil)
	request.RemoteAddr = "203.0.113.10:53000"
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong caller status=%d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/health", nil)
	request.RemoteAddr = "192.0.2.10:53000"
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHTTPRejectsCIDR(t *testing.T) {
	agent := newAgent(log.New(io.Discard, "", 0), time.Now)
	request := httptest.NewRequest(http.MethodPost, "/block", strings.NewReader(`{"ip":"192.0.2.0/24","ttl":"1h"}`))
	recorder := httptest.NewRecorder()
	agent.handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func requestJSON(t *testing.T, method, url, body string, wantStatus int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", response.StatusCode, wantStatus, responseBody)
	}
	return responseBody
}
