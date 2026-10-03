package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAuthLoginCreatesAuthenticatedSessionAndEnforcesCSRF(t *testing.T) {
	statePath := t.TempDir() + "/auth.json"
	if err := bootstrapAdminFromStdin(statePath, "test-admin", strings.NewReader("test-only-password!\n")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	manager, err := newAuthManager(statePath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	server := &server{auth: manager}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"test-admin","password":"test-only-password!"}`))
	login.RemoteAddr = "192.0.2.10:40000"
	loginResponse := httptest.NewRecorder()
	server.authLogin(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var loginBody authSessionResponse
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	if loginBody.State != sessionAuthenticated || loginBody.CSRFToken == "" {
		t.Fatalf("unexpected login response: %+v", loginBody)
	}
	cookie := loginResponse.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}

	protected := server.requireAuthentication(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	unauthenticated := httptest.NewRecorder()
	protected.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", unauthenticated.Code)
	}

	missingCSRFRequest := httptest.NewRequest(http.MethodPost, "/api/v1/control", nil)
	missingCSRFRequest.AddCookie(cookie)
	missingCSRF := httptest.NewRecorder()
	protected.ServeHTTP(missingCSRF, missingCSRFRequest)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", missingCSRF.Code)
	}

	validRequest := httptest.NewRequest(http.MethodPost, "/api/v1/control", nil)
	validRequest.AddCookie(cookie)
	validRequest.Header.Set("X-CSRF-Token", loginBody.CSRFToken)
	validResponse := httptest.NewRecorder()
	protected.ServeHTTP(validResponse, validRequest)
	if validResponse.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d body=%s", validResponse.Code, validResponse.Body.String())
	}
}

func TestAuthStateUsesArgon2idAndAdminLifecycle(t *testing.T) {
	statePath := t.TempDir() + "/auth.json"
	store, err := loadAuthStore(statePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.create("first-admin", "correct horse battery", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "correct horse battery") || !strings.Contains(string(data), "$argon2id$") {
		t.Fatalf("password storage is not Argon2id-only: %s", data)
	}
	if err := store.create("second-admin", "another secure password", false); err != nil {
		t.Fatal(err)
	}
	if err := store.setEnabled("first-admin", false); err != nil {
		t.Fatal(err)
	}
	if err := store.delete("second-admin"); err == nil {
		t.Fatal("deleted the last enabled Admin")
	}
	if err := store.setEnabled("first-admin", true); err != nil {
		t.Fatal(err)
	}
	if err := store.delete("second-admin"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	manager := &authManager{now: func() time.Time { return now }, attempts: make(map[string]loginAttempt), sessions: make(map[string]*authSessionRecord)}
	for range 5 {
		manager.loginFailed("client|admin")
	}
	if allowed, _ := manager.loginAllowed("client|admin"); allowed {
		t.Fatal("rate limiter allowed a sixth attempt")
	}
	now = now.Add(16 * time.Minute)
	if allowed, _ := manager.loginAllowed("client|admin"); !allowed {
		t.Fatal("rate limiter did not recover after lockout")
	}
}

func TestAuthenticatedSessionExpires(t *testing.T) {
	statePath := t.TempDir() + "/auth.json"
	if err := bootstrapAdminFromStdin(statePath, "test-admin", strings.NewReader("test-only-password!\n")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	manager, err := newAuthManager(statePath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	user, _ := manager.store.user("test-admin")
	session, err := manager.newSession(user)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: session.ID})
	now = now.Add(sessionLifetime + time.Second)
	if _, ok := manager.session(request); ok {
		t.Fatal("expired session accepted")
	}
}

func TestAuthStateV1MigrationPreservesPasswordAndRemovesTOTP(t *testing.T) {
	statePath := t.TempDir() + "/auth.json"
	hash, err := hashPassword("test-only-password!")
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyAuthState{Version: 1, Users: map[string]legacyAuthUser{
		"test-admin": {
			Username: "test-admin", PasswordHash: hash, Role: "Admin", Enabled: true,
			TOTPSecret: "JBSWY3DPEHPK3PXP", LastTOTPCounter: 123,
			CreatedAt: time.Date(2026, 10, 1, 1, 2, 3, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 2, 1, 2, 3, 0, time.UTC),
		},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := loadAuthStore(statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	user, ok := store.user("test-admin")
	if !ok || user.PasswordHash != hash || !verifyPassword("test-only-password!", user.PasswordHash) {
		t.Fatal("migration did not preserve the existing Admin password hash")
	}
	migrated, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migrated), `"version": 2`) || strings.Contains(strings.ToLower(string(migrated)), "totp") {
		t.Fatalf("unexpected migrated state: %s", migrated)
	}
}
