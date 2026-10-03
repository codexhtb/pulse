package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	desiredBlocked   = "blocked"
	desiredUnblocked = "unblocked"
	applyPending     = "pending"
	applyApplied     = "applied"
	applyError       = "error"
	applyDisabled    = "disabled"
)

type agentClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type dnsBlock struct {
	IP        string     `json:"ip"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Duration  string     `json:"duration"`
	Reason    string     `json:"reason"`
	Status    string     `json:"status"`
	Result    string     `json:"result"`
}

type dnsBlockRequest struct {
	Duration string `json:"duration"`
	Reason   string `json:"reason"`
}

type agentHealth struct {
	Status         string     `json:"status"`
	Mode           string     `json:"mode"`
	LastApplyError string     `json:"last_apply_error"`
	LastAppliedAt  *time.Time `json:"last_applied_at"`
}

func newAgentHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: 4 * time.Second,
		},
	}
}

func (c *agentClient) request(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var apiError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(payload, &apiError) == nil && apiError.Error != "" {
			return fmt.Errorf("agent status %d: %s", response.StatusCode, apiError.Error)
		}
		return fmt.Errorf("agent status %d", response.StatusCode)
	}
	if target != nil {
		if err := json.Unmarshal(payload, target); err != nil {
			return err
		}
	}
	return nil
}

func (c *agentClient) block(ctx context.Context, ip string, request dnsBlockRequest) (dnsBlock, error) {
	var result dnsBlock
	err := c.request(ctx, http.MethodPost, "/block", map[string]string{"ip": ip, "ttl": request.Duration, "reason": request.Reason}, &result)
	return result, err
}

func (c *agentClient) unblock(ctx context.Context, ip string) error {
	return c.request(ctx, http.MethodPost, "/unblock", map[string]string{"ip": ip}, nil)
}

func (c *agentClient) health(ctx context.Context) (agentHealth, error) {
	var health agentHealth
	err := c.request(ctx, http.MethodGet, "/health", nil, &health)
	return health, err
}

type nodeHealthState struct {
	Status         string     `json:"status"`
	Mode           string     `json:"mode,omitempty"`
	LastCheckedAt  *time.Time `json:"last_checked_at"`
	LastHealthyAt  *time.Time `json:"last_healthy_at"`
	LastError      string     `json:"last_error,omitempty"`
	LastApplyError string     `json:"last_apply_error,omitempty"`
}

type controlNodeView struct {
	ID             string          `json:"id"`
	DisplayName    string          `json:"display_name"`
	SourceIdentity string          `json:"source_identity"`
	DNSServiceIP   string          `json:"dns_service_ip"`
	ControlEnabled bool            `json:"control_enabled"`
	ControlStatus  string          `json:"control_status"`
	Health         nodeHealthState `json:"health"`
	Action         string          `json:"action,omitempty"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	UpdatedAt      *time.Time      `json:"updated_at"`
	LastError      string          `json:"last_error,omitempty"`
	NextRetryAt    *time.Time      `json:"next_retry_at,omitempty"`
	AgentResult    string          `json:"agent_result,omitempty"`
}

type dnsControlView struct {
	IP        string            `json:"ip"`
	Blocked   bool              `json:"blocked"`
	Desired   string            `json:"desired"`
	Status    string            `json:"status"`
	CreatedAt *time.Time        `json:"created_at"`
	UpdatedAt *time.Time        `json:"updated_at"`
	ExpiresAt *time.Time        `json:"expires_at"`
	Duration  string            `json:"duration,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Nodes     []controlNodeView `json:"nodes"`
}

type controlPlane struct {
	mu             sync.RWMutex
	operationMu    sync.Mutex
	nodes          []*controlNode
	state          durableControlState
	store          *controlStateStore
	health         map[string]nodeHealthState
	now            func() time.Time
	retryBase      time.Duration
	retryMax       time.Duration
	reconcileEvery time.Duration
	healthEvery    time.Duration
	logger         *log.Logger
	wake           chan struct{}
}

func newControlPlane(nodes []*controlNode, store *controlStateStore, now func() time.Time, logger *log.Logger) (*controlPlane, error) {
	if now == nil {
		now = time.Now
	}
	state, err := store.load()
	if err != nil {
		return nil, fmt.Errorf("load durable DNS control state: %w", err)
	}
	plane := &controlPlane{
		nodes: nodes, state: state, store: store, health: make(map[string]nodeHealthState), now: now,
		retryBase: 2 * time.Second, retryMax: time.Minute, reconcileEvery: time.Second, healthEvery: 10 * time.Second,
		logger: logger, wake: make(chan struct{}, 1),
	}
	if err := plane.alignStateWithNodes(); err != nil {
		return nil, err
	}
	return plane, nil
}

func (p *controlPlane) alignStateWithNodes() error {
	p.operationMu.Lock()
	defer p.operationMu.Unlock()
	now := p.now().UTC()
	changed := false
	candidate := cloneControlState(p.state)
	for ip, record := range candidate.Records {
		if record.Nodes == nil {
			record.Nodes = make(map[string]nodeApplyResult)
		}
		valid := make(map[string]struct{}, len(p.nodes))
		for _, node := range p.nodes {
			valid[node.config.ID] = struct{}{}
			result, exists := record.Nodes[node.config.ID]
			desiredStatus := applyPending
			if !node.config.ControlEnabled {
				desiredStatus = applyDisabled
			}
			if !exists || (result.Status == applyDisabled && node.config.ControlEnabled) || (result.Status != applyDisabled && !node.config.ControlEnabled) {
				record.Nodes[node.config.ID] = nodeApplyResult{NodeID: node.config.ID, Action: record.Desired, Status: desiredStatus, UpdatedAt: now}
				changed = true
			}
		}
		for nodeID := range record.Nodes {
			if _, exists := valid[nodeID]; !exists {
				delete(record.Nodes, nodeID)
				changed = true
			}
		}
		candidate.Records[ip] = record
	}
	if changed {
		if err := p.store.save(candidate); err != nil {
			return err
		}
		p.state = candidate
	}
	return nil
}

func (p *controlPlane) start(ctx context.Context) {
	go func() {
		reconcileTicker := time.NewTicker(p.reconcileEvery)
		healthTicker := time.NewTicker(p.healthEvery)
		defer reconcileTicker.Stop()
		defer healthTicker.Stop()
		p.reconcileAll(ctx)
		p.refreshHealth(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
				p.reconcileAll(ctx)
			case <-reconcileTicker.C:
				p.reconcileAll(ctx)
			case <-healthTicker.C:
				p.refreshHealth(ctx)
			}
		}
	}()
}

func (p *controlPlane) trigger() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func normalizeControlIP(raw string) (string, error) {
	if strings.Contains(strings.TrimSpace(raw), "/") {
		return "", fmt.Errorf("CIDR prefixes are not allowed")
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid client IP")
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return "", fmt.Errorf("only IPv4 clients can be blocked")
	}
	return addr.String(), nil
}

func parseControlDuration(value string, now time.Time) (string, *time.Time, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1h":
		expires := now.Add(time.Hour)
		return "1h", &expires, nil
	case "24h":
		expires := now.Add(24 * time.Hour)
		return "24h", &expires, nil
	case "permanent":
		return "permanent", nil, nil
	default:
		return "", nil, fmt.Errorf("duration must be one of: 1h, 24h, permanent")
	}
}

func (p *controlPlane) setBlock(ctx context.Context, ip string, request dnsBlockRequest) (dnsControlView, error) {
	now := p.now().UTC()
	duration, expiresAt, err := parseControlDuration(request.Duration, now)
	if err != nil {
		return dnsControlView{}, err
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "manual"
	}
	if len(reason) > 256 {
		return dnsControlView{}, fmt.Errorf("reason must not exceed 256 characters")
	}
	return p.setDesired(ctx, desiredDNSState{IP: ip, Desired: desiredBlocked, CreatedAt: now, UpdatedAt: now, ExpiresAt: expiresAt, Duration: duration, Reason: reason})
}

func (p *controlPlane) setUnblock(ctx context.Context, ip string) (dnsControlView, error) {
	now := p.now().UTC()
	record := desiredDNSState{IP: ip, Desired: desiredUnblocked, CreatedAt: now, UpdatedAt: now, Reason: "manual"}
	p.mu.RLock()
	if previous, exists := p.state.Records[ip]; exists {
		record.CreatedAt = previous.CreatedAt
		record.Reason = previous.Reason
	}
	p.mu.RUnlock()
	return p.setDesired(ctx, record)
}

func (p *controlPlane) setDesired(ctx context.Context, record desiredDNSState) (dnsControlView, error) {
	p.operationMu.Lock()
	defer p.operationMu.Unlock()
	now := p.now().UTC()
	record.Nodes = make(map[string]nodeApplyResult, len(p.nodes))
	for _, node := range p.nodes {
		status := applyPending
		if !node.config.ControlEnabled {
			status = applyDisabled
		}
		record.Nodes[node.config.ID] = nodeApplyResult{NodeID: node.config.ID, Action: record.Desired, Status: status, UpdatedAt: now}
	}
	p.mu.Lock()
	candidate := cloneControlState(p.state)
	candidate.Records[record.IP] = record
	if err := p.store.save(candidate); err != nil {
		p.mu.Unlock()
		return dnsControlView{}, fmt.Errorf("persist desired DNS state: %w", err)
	}
	p.state = candidate
	p.mu.Unlock()
	p.reconcileRecordLocked(ctx, record.IP, true)
	return p.view(record.IP), nil
}

func (p *controlPlane) get(ip string) dnsControlView { return p.view(ip) }

func (p *controlPlane) list() []dnsControlView {
	p.mu.RLock()
	ips := make([]string, 0, len(p.state.Records))
	for ip := range p.state.Records {
		ips = append(ips, ip)
	}
	p.mu.RUnlock()
	sort.Strings(ips)
	result := make([]dnsControlView, 0, len(ips))
	for _, ip := range ips {
		result = append(result, p.view(ip))
	}
	return result
}

func (p *controlPlane) reconcileAll(ctx context.Context) {
	p.operationMu.Lock()
	defer p.operationMu.Unlock()
	p.mu.Lock()
	now := p.now().UTC()
	candidate := cloneControlState(p.state)
	changed := false
	for ip, record := range candidate.Records {
		if record.Desired == desiredBlocked && record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			record.Desired = desiredUnblocked
			record.UpdatedAt = now
			for _, node := range p.nodes {
				status := applyPending
				if !node.config.ControlEnabled {
					status = applyDisabled
				}
				record.Nodes[node.config.ID] = nodeApplyResult{NodeID: node.config.ID, Action: desiredUnblocked, Status: status, UpdatedAt: now}
			}
			candidate.Records[ip] = record
			changed = true
		}
	}
	if changed {
		if err := p.store.save(candidate); err != nil {
			if p.logger != nil {
				p.logger.Printf("control persist expiry error=%v", err)
			}
			p.mu.Unlock()
			return
		}
		p.state = candidate
	}
	ips := make([]string, 0, len(p.state.Records))
	for ip := range p.state.Records {
		ips = append(ips, ip)
	}
	p.mu.Unlock()
	for _, ip := range ips {
		if ctx.Err() != nil {
			return
		}
		p.reconcileRecordLocked(ctx, ip, false)
	}
}

type nodeAttempt struct {
	node   *controlNode
	result nodeApplyResult
}

func (p *controlPlane) reconcileRecordLocked(ctx context.Context, ip string, force bool) {
	p.mu.RLock()
	record, exists := p.state.Records[ip]
	p.mu.RUnlock()
	if !exists {
		return
	}
	now := p.now().UTC()
	attempts := make([]nodeAttempt, 0, len(p.nodes))
	for _, node := range p.nodes {
		result := record.Nodes[node.config.ID]
		if !node.config.ControlEnabled || result.Status == applyApplied || (!force && result.NextRetryAt != nil && result.NextRetryAt.After(now)) {
			continue
		}
		attempts = append(attempts, nodeAttempt{node: node, result: result})
	}
	if len(attempts) == 0 {
		return
	}
	type outcome struct {
		nodeID      string
		agentResult string
		err         error
	}
	outcomes := make(chan outcome, len(attempts))
	var wg sync.WaitGroup
	for _, attempt := range attempts {
		wg.Add(1)
		go func(attempt nodeAttempt) {
			defer wg.Done()
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if record.Desired == desiredBlocked {
				block, err := attempt.node.client.block(callCtx, record.IP, dnsBlockRequest{Duration: record.Duration, Reason: record.Reason})
				outcomes <- outcome{nodeID: attempt.node.config.ID, agentResult: block.Result, err: err}
				return
			}
			err := attempt.node.client.unblock(callCtx, record.IP)
			outcomes <- outcome{nodeID: attempt.node.config.ID, agentResult: "removed", err: err}
		}(attempt)
	}
	wg.Wait()
	close(outcomes)

	p.mu.Lock()
	candidate := cloneControlState(p.state)
	latest, exists := candidate.Records[ip]
	if !exists || latest.Desired != record.Desired || !latest.UpdatedAt.Equal(record.UpdatedAt) {
		p.mu.Unlock()
		p.trigger()
		return
	}
	for outcome := range outcomes {
		result := latest.Nodes[outcome.nodeID]
		result.Attempts++
		result.UpdatedAt = p.now().UTC()
		if outcome.err != nil {
			result.Status = applyError
			result.LastError = outcome.err.Error()
			delay := p.retryDelay(result.Attempts)
			retryAt := result.UpdatedAt.Add(delay)
			result.NextRetryAt = &retryAt
			result.AgentResult = ""
			if p.logger != nil {
				p.logger.Printf("control reconcile node=%s ip=%s action=%s status=error retry_in=%s error=%q", outcome.nodeID, ip, record.Desired, delay, outcome.err)
			}
		} else {
			result.Status = applyApplied
			result.LastError = ""
			result.NextRetryAt = nil
			result.AgentResult = outcome.agentResult
			if p.logger != nil {
				p.logger.Printf("control reconcile node=%s ip=%s action=%s status=applied", outcome.nodeID, ip, record.Desired)
			}
		}
		latest.Nodes[outcome.nodeID] = result
	}
	candidate.Records[ip] = latest
	if err := p.store.save(candidate); err != nil {
		if p.logger != nil {
			p.logger.Printf("control persist reconcile error=%v", err)
		}
		p.mu.Unlock()
		return
	}
	p.state = candidate
	p.mu.Unlock()
}

func (p *controlPlane) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	exponent := math.Min(float64(attempt-1), 20)
	delay := time.Duration(float64(p.retryBase) * math.Pow(2, exponent))
	if delay > p.retryMax {
		return p.retryMax
	}
	return delay
}

func (p *controlPlane) refreshHealth(ctx context.Context) {
	type outcome struct {
		nodeID string
		health agentHealth
		err    error
	}
	results := make(chan outcome, len(p.nodes))
	var wg sync.WaitGroup
	for _, node := range p.nodes {
		if !node.config.ControlEnabled {
			continue
		}
		wg.Add(1)
		go func(node *controlNode) {
			defer wg.Done()
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			health, err := node.client.health(callCtx)
			results <- outcome{nodeID: node.config.ID, health: health, err: err}
		}(node)
	}
	wg.Wait()
	close(results)
	now := p.now().UTC()
	p.mu.Lock()
	for _, node := range p.nodes {
		if !node.config.ControlEnabled {
			p.health[node.config.ID] = nodeHealthState{Status: applyDisabled}
		}
	}
	for result := range results {
		checked := now
		state := p.health[result.nodeID]
		state.LastCheckedAt = &checked
		if result.err != nil {
			state.Status = "unavailable"
			state.LastError = result.err.Error()
		} else {
			state.Status = result.health.Status
			state.Mode = result.health.Mode
			state.LastError = ""
			state.LastApplyError = result.health.LastApplyError
			healthy := now
			state.LastHealthyAt = &healthy
		}
		p.health[result.nodeID] = state
	}
	p.mu.Unlock()
}

func (p *controlPlane) view(ip string) dnsControlView {
	p.mu.RLock()
	record, exists := p.state.Records[ip]
	health := make(map[string]nodeHealthState, len(p.health))
	for id, state := range p.health {
		health[id] = state
	}
	p.mu.RUnlock()
	view := dnsControlView{IP: ip, Desired: desiredUnblocked, Status: applyApplied}
	if exists {
		view.Blocked = record.Desired == desiredBlocked
		view.Desired = record.Desired
		view.CreatedAt = &record.CreatedAt
		view.UpdatedAt = &record.UpdatedAt
		view.ExpiresAt = record.ExpiresAt
		view.Duration = record.Duration
		view.Reason = record.Reason
	}
	statuses := make([]string, 0, len(p.nodes))
	for _, node := range p.nodes {
		nodeView := controlNodeView{
			ID: node.config.ID, DisplayName: node.config.DisplayName, SourceIdentity: node.config.SourceIdentity,
			DNSServiceIP: node.config.DNSServiceIP, ControlEnabled: node.config.ControlEnabled,
			ControlStatus: "enabled", Health: health[node.config.ID], Status: applyApplied,
		}
		if !node.config.ControlEnabled {
			nodeView.ControlStatus = applyDisabled
			nodeView.Status = applyDisabled
		}
		if exists {
			result := record.Nodes[node.config.ID]
			nodeView.Action = result.Action
			nodeView.Status = result.Status
			nodeView.Attempts = result.Attempts
			if !result.UpdatedAt.IsZero() {
				t := result.UpdatedAt
				nodeView.UpdatedAt = &t
			}
			nodeView.LastError = result.LastError
			nodeView.NextRetryAt = result.NextRetryAt
			nodeView.AgentResult = result.AgentResult
		}
		view.Nodes = append(view.Nodes, nodeView)
		if node.config.ControlEnabled {
			statuses = append(statuses, nodeView.Status)
		}
	}
	view.Status = aggregateApplyStatus(statuses)
	return view
}

func (p *controlPlane) nodeViews() []controlNodeView {
	view := p.view("")
	for index := range view.Nodes {
		view.Nodes[index].Action = ""
		view.Nodes[index].Status = view.Nodes[index].ControlStatus
		view.Nodes[index].Attempts = 0
		view.Nodes[index].UpdatedAt = nil
		view.Nodes[index].LastError = ""
		view.Nodes[index].NextRetryAt = nil
		view.Nodes[index].AgentResult = ""
	}
	return view.Nodes
}

func aggregateApplyStatus(statuses []string) string {
	if len(statuses) == 0 {
		return applyDisabled
	}
	applied, pending, failed := 0, 0, 0
	for _, status := range statuses {
		switch status {
		case applyApplied:
			applied++
		case applyError:
			failed++
		default:
			pending++
		}
	}
	switch {
	case applied == len(statuses):
		return applyApplied
	case failed > 0 && applied > 0:
		return "partial"
	case failed == len(statuses):
		return applyError
	case pending > 0:
		return applyPending
	default:
		return applyPending
	}
}

func (s *server) dnsBlockRoute(w http.ResponseWriter, r *http.Request, clientIP string) {
	if s.control == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("DNS control plane is not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
	defer cancel()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.control.get(clientIP))
	case http.MethodPost:
		var request dnsBlockRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
			return
		}
		view, err := s.control.setBlock(ctx, clientIP, request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodDelete:
		view, err := s.control.setUnblock(ctx, clientIP)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}
}

func (s *server) dnsBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	if s.control == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("DNS control plane is not configured"))
		return
	}
	records := s.control.list()
	writeJSON(w, http.StatusOK, map[string]any{"blocks": records, "count": len(records)})
}

func (s *server) controlNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	if s.control == nil {
		writeJSON(w, http.StatusOK, map[string]any{"nodes": []controlNodeView{}, "count": 0})
		return
	}
	nodes := s.control.nodeViews()
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "count": len(nodes)})
}
