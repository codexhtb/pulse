package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	authStateVersion = 2
	sessionCookie    = "pulse_session"
	sessionLifetime  = 12 * time.Hour
	sessionIdleLimit = 30 * time.Minute
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,63}$`)

type authUser struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// legacyAuthUser exists only to migrate the previous TOTP-enabled state without
// changing password hashes or requiring an Admin password reset.
type legacyAuthUser struct {
	Username        string    `json:"username"`
	PasswordHash    string    `json:"password_hash"`
	Role            string    `json:"role"`
	Enabled         bool      `json:"enabled"`
	TOTPSecret      string    `json:"totp_secret,omitempty"`
	LastTOTPCounter int64     `json:"last_totp_counter,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type legacyAuthState struct {
	Version int                       `json:"version"`
	Users   map[string]legacyAuthUser `json:"users"`
}

type authState struct {
	Version int                 `json:"version"`
	Users   map[string]authUser `json:"users"`
}

type authStore struct {
	mu    sync.Mutex
	path  string
	state authState
}

type authSessionState string

const sessionAuthenticated authSessionState = "authenticated"

type authSessionRecord struct {
	ID         string
	Username   string
	State      authSessionState
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

type loginAttempt struct {
	Failures     []time.Time
	BlockedUntil time.Time
}

type authManager struct {
	store     *authStore
	now       func() time.Time
	dummyHash string

	mu       sync.Mutex
	sessions map[string]*authSessionRecord
	attempts map[string]loginAttempt
}

type authContextKey struct{}

type authSessionResponse struct {
	State     authSessionState `json:"state"`
	CSRFToken string           `json:"csrf_token"`
	User      adminUserView    `json:"user"`
}

type adminUserView struct {
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func bootstrapAdminFromStdin(path, username string, input io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(input, 1025))
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if len(data) > 1024 {
		return errors.New("password is too long")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if err := validatePassword(password); err != nil {
		return err
	}
	store, err := loadAuthStore(path, true)
	if err != nil {
		return err
	}
	return store.create(username, password, true)
}

func newAuthManager(path string, now func() time.Time) (*authManager, error) {
	store, err := loadAuthStore(path, false)
	if err != nil {
		return nil, err
	}
	dummyHash, err := hashPassword("not-a-real-pulse-password")
	if err != nil {
		return nil, err
	}
	return &authManager{
		store: store, now: now, dummyHash: dummyHash,
		sessions: make(map[string]*authSessionRecord), attempts: make(map[string]loginAttempt),
	}, nil
}

func loadAuthStore(path string, allowMissing bool) (*authStore, error) {
	store := &authStore{path: path, state: authState{Version: authStateVersion, Users: make(map[string]authUser)}}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if allowMissing {
			return store, nil
		}
		return nil, fmt.Errorf("authentication state is missing: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("stat authentication state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("authentication state must be a regular file inaccessible to group/others")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open authentication state: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read authentication state: %w", err)
	}
	if len(data) > 4<<20 {
		return nil, errors.New("authentication state is too large")
	}
	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode authentication state version: %w", err)
	}
	migrated := false
	switch envelope.Version {
	case 1:
		var legacy legacyAuthState
		if err := decodeOneJSON(data, &legacy); err != nil {
			return nil, fmt.Errorf("decode legacy authentication state: %w", err)
		}
		for key, user := range legacy.Users {
			store.state.Users[key] = authUser{Username: user.Username, PasswordHash: user.PasswordHash, Role: user.Role, Enabled: user.Enabled, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
		}
		migrated = true
	case authStateVersion:
		if err := decodeOneJSON(data, &store.state); err != nil {
			return nil, fmt.Errorf("decode authentication state: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported authentication state version %d", envelope.Version)
	}
	if store.state.Version != authStateVersion || len(store.state.Users) == 0 {
		return nil, errors.New("authentication state has no valid Admin users")
	}
	for key, user := range store.state.Users {
		if key != normalizeUsername(user.Username) || user.Role != "Admin" || !usernamePattern.MatchString(user.Username) {
			return nil, fmt.Errorf("invalid Admin record %q", key)
		}
		if _, err := parsePasswordHash(user.PasswordHash); err != nil {
			return nil, fmt.Errorf("invalid password hash for %q: %w", key, err)
		}
	}
	if migrated {
		if err := store.saveLocked(); err != nil {
			return nil, fmt.Errorf("migrate authentication state: %w", err)
		}
	}
	return store, nil
}

func decodeOneJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("authentication state must contain one JSON object")
	}
	return nil
}

func (s *authStore) saveLocked() error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create auth state directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".auth-*.json")
	if err != nil {
		return fmt.Errorf("create temporary auth state: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.state); err != nil {
		temp.Close()
		return fmt.Errorf("encode auth state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync auth state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace auth state: %w", err)
	}
	return nil
}

func (s *authStore) create(username, password string, initial bool) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("username must be 3-64 characters using letters, digits, dot, underscore, or hyphen")
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	key := normalizeUsername(username)
	s.mu.Lock()
	defer s.mu.Unlock()
	if initial && len(s.state.Users) != 0 {
		return errors.New("authentication state already contains users")
	}
	if _, exists := s.state.Users[key]; exists {
		return errors.New("Admin already exists")
	}
	s.state.Users[key] = authUser{Username: username, PasswordHash: hash, Role: "Admin", Enabled: true, CreatedAt: now, UpdatedAt: now}
	return s.saveLocked()
}

func (s *authStore) user(username string) (authUser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.state.Users[normalizeUsername(username)]
	return user, ok
}

func (s *authStore) list() []adminUserView {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]adminUserView, 0, len(s.state.Users))
	for _, user := range s.state.Users {
		users = append(users, viewAdmin(user))
	}
	for i := 0; i < len(users); i++ {
		for j := i + 1; j < len(users); j++ {
			if strings.ToLower(users[j].Username) < strings.ToLower(users[i].Username) {
				users[i], users[j] = users[j], users[i]
			}
		}
	}
	return users
}

func (s *authStore) setEnabled(username string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeUsername(username)
	user, ok := s.state.Users[key]
	if !ok {
		return os.ErrNotExist
	}
	if !enabled && user.Enabled && s.enabledCountLocked() == 1 {
		return errors.New("cannot disable the last enabled Admin")
	}
	user.Enabled = enabled
	user.UpdatedAt = time.Now().UTC()
	s.state.Users[key] = user
	return s.saveLocked()
}

func (s *authStore) resetPassword(username, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeUsername(username)
	user, ok := s.state.Users[key]
	if !ok {
		return os.ErrNotExist
	}
	user.PasswordHash = hash
	user.UpdatedAt = time.Now().UTC()
	s.state.Users[key] = user
	return s.saveLocked()
}

func (s *authStore) delete(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeUsername(username)
	user, ok := s.state.Users[key]
	if !ok {
		return os.ErrNotExist
	}
	if user.Enabled && s.enabledCountLocked() == 1 {
		return errors.New("cannot delete the last enabled Admin")
	}
	delete(s.state.Users, key)
	return s.saveLocked()
}

func (s *authStore) enabledCountLocked() int {
	count := 0
	for _, user := range s.state.Users {
		if user.Enabled {
			count++
		}
	}
	return count
}

func (a *authManager) newSession(user authUser) (*authSessionRecord, error) {
	now := a.now().UTC()
	id, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	session := &authSessionRecord{ID: id, Username: user.Username, State: sessionAuthenticated, CSRFToken: csrf, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(sessionLifetime)}
	a.mu.Lock()
	a.sessions[id] = session
	a.pruneSessionsLocked(now)
	a.mu.Unlock()
	return session, nil
}

func (a *authManager) session(r *http.Request) (*authSessionRecord, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	now := a.now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[cookie.Value]
	if !ok || now.After(session.ExpiresAt) || now.Sub(session.LastSeenAt) > sessionIdleLimit {
		delete(a.sessions, cookie.Value)
		return nil, false
	}
	user, ok := a.store.user(session.Username)
	if !ok || !user.Enabled || session.State != sessionAuthenticated {
		if !ok || !user.Enabled {
			delete(a.sessions, cookie.Value)
		}
		return nil, false
	}
	session.LastSeenAt = now
	copy := *session
	return &copy, true
}

func (a *authManager) revokeSession(id string) {
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
}

func (a *authManager) revokeUserSessions(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, session := range a.sessions {
		if strings.EqualFold(session.Username, username) {
			delete(a.sessions, id)
		}
	}
}

func (a *authManager) pruneSessionsLocked(now time.Time) {
	for id, session := range a.sessions {
		if now.After(session.ExpiresAt) || now.Sub(session.LastSeenAt) > sessionIdleLimit {
			delete(a.sessions, id)
		}
	}
}

func (a *authManager) loginAllowed(key string) (bool, time.Duration) {
	now := a.now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	attempt := a.attempts[key]
	if now.Before(attempt.BlockedUntil) {
		return false, attempt.BlockedUntil.Sub(now)
	}
	cutoff := now.Add(-15 * time.Minute)
	kept := attempt.Failures[:0]
	for _, failure := range attempt.Failures {
		if failure.After(cutoff) {
			kept = append(kept, failure)
		}
	}
	attempt.Failures = kept
	a.attempts[key] = attempt
	return true, 0
}

func (a *authManager) loginFailed(key string) {
	now := a.now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	attempt := a.attempts[key]
	attempt.Failures = append(attempt.Failures, now)
	if len(attempt.Failures) >= 5 {
		attempt.BlockedUntil = now.Add(15 * time.Minute)
		attempt.Failures = nil
	}
	a.attempts[key] = attempt
}

func (a *authManager) loginSucceeded(key string) {
	a.mu.Lock()
	delete(a.attempts, key)
	a.mu.Unlock()
}

func (s *server) authLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeAuthJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	key := strings.ToLower(clientAddress(r) + "|" + normalizeUsername(request.Username))
	if allowed, retry := s.auth.loginAllowed(key); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		writeError(w, http.StatusTooManyRequests, errors.New("too many login attempts; try again later"))
		return
	}
	user, exists := s.auth.store.user(request.Username)
	hash := s.auth.dummyHash
	if exists {
		hash = user.PasswordHash
	}
	passwordOK := verifyPassword(request.Password, hash)
	if !exists || !user.Enabled || !passwordOK {
		s.auth.loginFailed(key)
		writeError(w, http.StatusUnauthorized, errors.New("invalid username or password"))
		return
	}
	s.auth.loginSucceeded(key)
	session, err := s.auth.newSession(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("could not create session"))
		return
	}
	setSessionCookie(w, session.ID, sessionLifetime)
	writeJSON(w, http.StatusOK, sessionResponse(session, user))
}

func (s *server) authSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	session, ok := s.auth.session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	user, _ := s.auth.store.user(session.Username)
	writeJSON(w, http.StatusOK, sessionResponse(session, user))
}

func (s *server) authLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	session, ok := s.auth.session(r)
	if !ok {
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
		return
	}
	if !csrfMatches(r, session.CSRFToken) {
		writeError(w, http.StatusForbidden, errors.New("invalid CSRF token"))
		return
	}
	s.auth.revokeSession(session.ID)
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *server) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.auth.session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		if isMutating(r.Method) && !csrfMatches(r, session.CSRFToken) {
			writeError(w, http.StatusForbidden, errors.New("invalid CSRF token"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, session)))
	})
}

func (s *server) adminUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users := s.auth.store.list()
		writeJSON(w, http.StatusOK, map[string]any{"users": users, "count": len(users)})
	case http.MethodPost:
		var request struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := decodeAuthJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.auth.store.create(request.Username, request.Password, false); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		user, _ := s.auth.store.user(request.Username)
		writeJSON(w, http.StatusCreated, viewAdmin(user))
	default:
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
	}
}

func (s *server) adminUserRoute(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/users/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("Admin not found"))
		return
	}
	username, err := url.PathUnescape(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid username"))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var request struct {
				Enabled *bool `json:"enabled"`
			}
			if err := decodeAuthJSON(r, &request); err != nil || request.Enabled == nil {
				writeError(w, http.StatusBadRequest, errors.New("enabled is required"))
				return
			}
			if err := s.auth.store.setEnabled(username, *request.Enabled); err != nil {
				writeAdminMutationError(w, err)
				return
			}
			if !*request.Enabled {
				s.auth.revokeUserSessions(username)
			}
			user, _ := s.auth.store.user(username)
			writeJSON(w, http.StatusOK, viewAdmin(user))
		case http.MethodDelete:
			if err := s.auth.store.delete(username); err != nil {
				writeAdminMutationError(w, err)
				return
			}
			s.auth.revokeUserSessions(username)
			writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		default:
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		}
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, errors.New("Admin action not found"))
		return
	}
	switch parts[1] {
	case "password":
		var request struct {
			Password string `json:"password"`
		}
		if err := decodeAuthJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.auth.store.resetPassword(username, request.Password); err != nil {
			writeAdminMutationError(w, err)
			return
		}
		s.auth.revokeUserSessions(username)
		writeJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
	case "sessions":
		if _, ok := s.auth.store.user(username); !ok {
			writeError(w, http.StatusNotFound, errors.New("Admin not found"))
			return
		}
		s.auth.revokeUserSessions(username)
		writeJSON(w, http.StatusOK, map[string]string{"status": "sessions_revoked"})
	default:
		writeError(w, http.StatusNotFound, errors.New("Admin action not found"))
	}
}

func writeAdminMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, errors.New("Admin not found"))
		return
	}
	writeError(w, http.StatusConflict, err)
}

func decodeAuthJSON(r *http.Request, destination any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid JSON request")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func sessionResponse(session *authSessionRecord, user authUser) authSessionResponse {
	return authSessionResponse{State: session.State, CSRFToken: session.CSRFToken, User: viewAdmin(user)}
}

func viewAdmin(user authUser) adminUserView {
	return adminUserView{Username: user.Username, Role: "Admin", Enabled: user.Enabled, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
}

func setSessionCookie(w http.ResponseWriter, value string, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(lifetime.Seconds()), Expires: time.Now().Add(lifetime)})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func csrfMatches(r *http.Request, expected string) bool {
	provided := r.Header.Get("X-CSRF-Token")
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func isMutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func clientAddress(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(value) != nil {
		return value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func normalizeUsername(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 512 {
		return errors.New("password must be 12-512 characters")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

type passwordHashParameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	hash        []byte
}

func parsePasswordHash(encoded string) (passwordHashParameters, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return passwordHashParameters{}, errors.New("unsupported Argon2id encoding")
	}
	var parameters passwordHashParameters
	var parallelism uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parameters.memory, &parameters.iterations, &parallelism); err != nil || parallelism > 255 {
		return parameters, errors.New("invalid Argon2id parameters")
	}
	parameters.parallelism = uint8(parallelism)
	var err error
	parameters.salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return parameters, errors.New("invalid Argon2id salt")
	}
	parameters.hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(parameters.hash) == 0 || parameters.memory < 8*1024 || parameters.memory > 1024*1024 || parameters.iterations < 1 || parameters.iterations > 10 || parameters.parallelism < 1 || parameters.parallelism > 16 {
		return parameters, errors.New("invalid Argon2id hash")
	}
	return parameters, nil
}

func verifyPassword(password, encoded string) bool {
	parameters, err := parsePasswordHash(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), parameters.salt, parameters.iterations, parameters.memory, parameters.parallelism, uint32(len(parameters.hash)))
	return subtle.ConstantTimeCompare(actual, parameters.hash) == 1
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
