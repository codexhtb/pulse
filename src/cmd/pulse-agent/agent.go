package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	statusPending = "pending"
	statusApplied = "applied"
	statusError   = "error"
	reasonManual  = "manual"
)

var (
	errInvalidIP     = errors.New("invalid IP address")
	errCIDR          = errors.New("CIDR prefixes are not allowed")
	errIPv4Only      = errors.New("only IPv4 addresses are supported")
	errProtectedIP   = errors.New("IP address is protected and cannot be blocked")
	errInvalidTTL    = errors.New("ttl must be one of: 1h, 24h, permanent")
	errReasonTooLong = errors.New("reason must not exceed 256 characters")
	errFirewallApply = errors.New("firewall apply failed")
)

type blockRecord struct {
	IP        string     `json:"ip"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Duration  string     `json:"duration"`
	Reason    string     `json:"reason"`
	Status    string     `json:"status"`
	Result    string     `json:"result"`
}

type firewallBlock struct {
	IP        string     `json:"ip"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type firewall interface {
	Reconcile(context.Context, []firewallBlock) error
	Status(context.Context) (any, error)
}

type noopFirewall struct{}

func (noopFirewall) Reconcile(context.Context, []firewallBlock) error { return nil }
func (noopFirewall) Status(context.Context) (any, error) {
	return map[string]any{"mode": "memory"}, nil
}

type agent struct {
	mu            sync.Mutex
	operationMu   sync.Mutex
	blocks        map[netip.Addr]blockRecord
	protected     map[netip.Addr]struct{}
	now           func() time.Time
	logger        *log.Logger
	store         *stateStore
	firewall      firewall
	lastApplyErr  string
	lastAppliedAt *time.Time
	mode          string
}

func newAgent(logger *log.Logger, now func() time.Time) *agent {
	if now == nil {
		now = time.Now
	}
	return &agent{
		blocks:    make(map[netip.Addr]blockRecord),
		protected: make(map[netip.Addr]struct{}),
		now:       now,
		logger:    logger,
		firewall:  noopFirewall{},
		mode:      "enforcing",
	}
}

func (a *agent) setMode(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = mode
}

func (a *agent) setRuntime(store *stateStore, fw firewall, protected []netip.Addr) {
	a.store = store
	if fw != nil {
		a.firewall = fw
	}
	for _, addr := range protected {
		a.protected[addr.Unmap()] = struct{}{}
	}
}

func normalizeIP(raw string) (netip.Addr, error) {
	value := strings.TrimSpace(raw)
	if strings.Contains(value, "/") {
		return netip.Addr{}, errCIDR
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, errInvalidIP
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return netip.Addr{}, errIPv4Only
	}
	return addr, nil
}

func unsafeAddress(addr netip.Addr) bool {
	return addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr == netip.MustParseAddr("255.255.255.255")
}

func parseTTL(value string) (string, *time.Duration, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "1h":
		d := time.Hour
		return normalized, &d, nil
	case "24h":
		d := 24 * time.Hour
		return normalized, &d, nil
	case "permanent":
		return normalized, nil, nil
	default:
		return "", nil, errInvalidTTL
	}
}

func (a *agent) Health() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	status := "ok"
	if a.lastApplyErr != "" {
		status = "degraded"
	}
	return map[string]any{
		"service":          "pulse-agent",
		"status":           status,
		"mode":             a.mode,
		"time":             a.now().UTC(),
		"last_apply_error": a.lastApplyErr,
		"last_applied_at":  a.lastAppliedAt,
	}
}

func (a *agent) Block(rawIP, ttl, reason string) (blockRecord, error) {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()

	addr, err := normalizeIP(rawIP)
	if err != nil {
		return blockRecord{}, err
	}
	if unsafeAddress(addr) {
		return blockRecord{}, errProtectedIP
	}
	if _, protected := a.protected[addr]; protected {
		return blockRecord{}, errProtectedIP
	}
	durationName, duration, err := parseTTL(ttl)
	if err != nil {
		return blockRecord{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = reasonManual
	}
	if len(reason) > 256 {
		return blockRecord{}, errReasonTooLong
	}

	now := a.now().UTC()
	var expiresAt *time.Time
	if duration != nil {
		expires := now.Add(*duration)
		expiresAt = &expires
	}
	record := blockRecord{
		IP:        addr.String(),
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
		Duration:  durationName,
		Reason:    reason,
		Status:    statusPending,
		Result:    "awaiting firewall reconciliation",
	}

	a.mu.Lock()
	a.blocks[addr] = record
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return record, err
	}

	applyErr := a.reconcileDesired()
	a.mu.Lock()
	record = a.blocks[addr]
	a.mu.Unlock()
	result := record.Result
	if applyErr != nil {
		result = applyErr.Error()
	}
	a.audit(auditEntry{Timestamp: now, IP: addr.String(), Action: "block", Reason: reason, Duration: durationName, Result: result})
	return record, applyErr
}

func (a *agent) Unblock(rawIP string) (blockRecord, bool, error) {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()

	addr, err := normalizeIP(rawIP)
	if err != nil {
		return blockRecord{}, false, err
	}
	a.mu.Lock()
	previous, found := a.blocks[addr]
	if found {
		delete(a.blocks, addr)
	}
	a.mu.Unlock()
	if !found {
		a.audit(auditEntry{Timestamp: a.now().UTC(), IP: addr.String(), Action: "unblock", Result: "not_found"})
		return blockRecord{IP: addr.String(), Status: statusApplied, Result: "not_found"}, false, nil
	}
	if err := a.persist(); err != nil {
		return previous, false, err
	}
	if err := a.reconcileDesired(); err != nil {
		previous.Status = statusError
		previous.Result = err.Error()
		previous.UpdatedAt = a.now().UTC()
		a.mu.Lock()
		a.blocks[addr] = previous
		a.mu.Unlock()
		_ = a.persist()
		a.audit(auditEntry{Timestamp: a.now().UTC(), IP: addr.String(), Action: "unblock", Reason: previous.Reason, Duration: previous.Duration, Result: err.Error()})
		return previous, false, err
	}
	result := blockRecord{IP: addr.String(), UpdatedAt: a.now().UTC(), Status: statusApplied, Result: "removed"}
	a.audit(auditEntry{Timestamp: result.UpdatedAt, IP: addr.String(), Action: "unblock", Reason: previous.Reason, Duration: previous.Duration, Result: result.Result})
	return result, true, nil
}

func (a *agent) List() []blockRecord {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	if a.removeExpired() {
		_ = a.persist()
		_ = a.reconcileDesired()
	}
	return a.snapshotRecords()
}

func (a *agent) Restore() error {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	if a.store != nil {
		records, err := a.store.load()
		if err != nil {
			return err
		}
		for _, record := range records {
			addr, err := normalizeIP(record.IP)
			if err != nil || unsafeAddress(addr) {
				continue
			}
			if _, protected := a.protected[addr]; protected {
				continue
			}
			a.blocks[addr] = record
		}
	}
	a.removeExpired()
	return a.reconcileDesired()
}

func (a *agent) Reconcile() error {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	a.removeExpired()
	return a.reconcileDesired()
}

func (a *agent) reconcileDesired() error {
	now := a.now().UTC()
	a.mu.Lock()
	blocks := make([]firewallBlock, 0, len(a.blocks))
	for _, record := range a.blocks {
		blocks = append(blocks, firewallBlock{IP: record.IP, ExpiresAt: record.ExpiresAt})
	}
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.firewall.Reconcile(ctx, blocks)

	a.mu.Lock()
	if err != nil {
		a.lastApplyErr = err.Error()
		for addr, record := range a.blocks {
			record.Status = statusError
			record.Result = err.Error()
			record.UpdatedAt = now
			a.blocks[addr] = record
		}
	} else {
		a.lastApplyErr = ""
		a.lastAppliedAt = &now
		for addr, record := range a.blocks {
			record.Status = statusApplied
			record.Result = "firewall state applied"
			record.UpdatedAt = now
			a.blocks[addr] = record
		}
	}
	a.mu.Unlock()
	_ = a.persist()
	if err != nil {
		return fmt.Errorf("%w: %v", errFirewallApply, err)
	}
	return nil
}

func (a *agent) removeExpired() bool {
	now := a.now().UTC()
	removed := false
	var audits []auditEntry
	a.mu.Lock()
	for addr, record := range a.blocks {
		if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			delete(a.blocks, addr)
			removed = true
			audits = append(audits, auditEntry{Timestamp: now, IP: addr.String(), Action: "expire", Reason: record.Reason, Duration: record.Duration, Result: "removed"})
		}
	}
	a.mu.Unlock()
	for _, entry := range audits {
		a.audit(entry)
	}
	return removed
}

func (a *agent) snapshotRecords() []blockRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]blockRecord, 0, len(a.blocks))
	for _, record := range a.blocks {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].IP < result[j].IP })
	return result
}

func (a *agent) persist() error {
	if a.store == nil {
		return nil
	}
	return a.store.save(a.snapshotRecords())
}

func (a *agent) audit(entry auditEntry) {
	if a.logger != nil {
		a.logger.Printf("audit ip=%s action=%s reason=%q duration=%s timestamp=%s result=%q", entry.IP, entry.Action, entry.Reason, entry.Duration, entry.Timestamp.Format(time.RFC3339Nano), entry.Result)
	}
	if a.store != nil {
		if err := a.store.appendAudit(entry); err != nil && a.logger != nil {
			a.logger.Printf("audit persist error=%v", err)
		}
	}
}

func parseProtectedIPs(value string) ([]netip.Addr, error) {
	var result []netip.Addr
	for _, raw := range strings.Split(value, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		addr, err := normalizeIP(raw)
		if err != nil {
			return nil, fmt.Errorf("protected IP %q: %w", raw, err)
		}
		result = append(result, addr)
	}
	return result, nil
}
