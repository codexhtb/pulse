package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

type fakeFirewall struct {
	blocks []firewallBlock
	err    error
}

func (f *fakeFirewall) Reconcile(_ context.Context, blocks []firewallBlock) error {
	f.blocks = append([]firewallBlock(nil), blocks...)
	return f.err
}

func (f *fakeFirewall) Status(context.Context) (any, error) { return nil, f.err }

func testAgent(now *time.Time) *agent {
	return newAgent(log.New(io.Discard, "", 0), func() time.Time { return *now })
}

func TestNormalizeIP(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "IPv4", input: "192.0.2.10", want: "192.0.2.10"},
		{name: "IPv4-mapped IPv6", input: "::ffff:192.0.2.10", want: "192.0.2.10"},
		{name: "invalid IP", input: "not-an-ip", wantErr: errInvalidIP},
		{name: "CIDR", input: "192.0.2.0/24", wantErr: errCIDR},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeIP(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("normalizeIP(%q) error=%v, want %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr == nil && got.String() != tt.want {
				t.Fatalf("normalizeIP(%q)=%q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBlockUnblockList(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	agent := testAgent(&now)

	record, err := agent.Block("::ffff:192.0.2.10", "24h", "test")
	if err != nil {
		t.Fatal(err)
	}
	if record.IP != "192.0.2.10" || record.Reason != "test" || record.Status != statusApplied {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.ExpiresAt == nil || !record.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("expires_at=%v, want %v", record.ExpiresAt, now.Add(24*time.Hour))
	}

	blocks := agent.List()
	if len(blocks) != 1 || blocks[0] != record {
		t.Fatalf("List()=%+v, want %+v", blocks, record)
	}

	_, removed, err := agent.Unblock("192.0.2.10")
	if err != nil || !removed {
		t.Fatalf("Unblock() removed=%t error=%v", removed, err)
	}
	if blocks := agent.List(); len(blocks) != 0 {
		t.Fatalf("List() after unblock=%+v, want empty", blocks)
	}
}

func TestProtectedAddressesCannotBeBlocked(t *testing.T) {
	now := time.Now()
	agent := testAgent(&now)
	agent.setRuntime(nil, nil, []netip.Addr{netip.MustParseAddr("192.0.2.53"), netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.1")})
	for _, ip := range []string{"127.0.0.1", "0.0.0.0", "169.254.1.1", "224.0.0.1", "192.0.2.53", "192.0.2.10", "192.0.2.1"} {
		if _, err := agent.Block(ip, "1h", "test"); !errors.Is(err, errProtectedIP) {
			t.Fatalf("Block(%s) error=%v, want protected error", ip, err)
		}
	}
}

func TestExpiry(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	agent := testAgent(&now)
	if _, err := agent.Block("198.51.100.20", "1h", ""); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Hour)
	if blocks := agent.List(); len(blocks) != 0 {
		t.Fatalf("expired block still listed: %+v", blocks)
	}
}

func TestPermanentBlockDoesNotExpire(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	agent := testAgent(&now)
	record, err := agent.Block("203.0.113.30", "permanent", "")
	if err != nil {
		t.Fatal(err)
	}
	if record.ExpiresAt != nil {
		t.Fatalf("permanent block expires_at=%v, want nil", record.ExpiresAt)
	}
	now = now.Add(365 * 24 * time.Hour)
	if blocks := agent.List(); len(blocks) != 1 {
		t.Fatalf("permanent block expired: %+v", blocks)
	}
}

func TestInvalidTTL(t *testing.T) {
	now := time.Now()
	agent := testAgent(&now)
	if _, err := agent.Block("192.0.2.10", "30m", ""); !errors.Is(err, errInvalidTTL) {
		t.Fatalf("Block() error=%v, want %v", err, errInvalidTTL)
	}
}

func TestHealthReportsConfiguredMode(t *testing.T) {
	agent := newAgent(nil, time.Now)
	agent.setMode("dry-run")
	if got := agent.Health()["mode"]; got != "dry-run" {
		t.Fatalf("mode=%v, want dry-run", got)
	}
}

func TestFirewallFailureIsVisibleAndPersisted(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firewall := &fakeFirewall{err: errors.New("nft failed")}
	agent := testAgent(&now)
	agent.setRuntime(newStateStore(filepath.Join(dir, "blocks.json"), filepath.Join(dir, "audit.jsonl")), firewall, nil)
	record, err := agent.Block("192.0.2.10", "1h", "test")
	if !errors.Is(err, errFirewallApply) || record.Status != statusError {
		t.Fatalf("record=%+v error=%v", record, err)
	}

	restored := testAgent(&now)
	restored.setRuntime(newStateStore(filepath.Join(dir, "blocks.json"), filepath.Join(dir, "audit.jsonl")), &fakeFirewall{}, nil)
	if err := restored.Restore(); err != nil {
		t.Fatal(err)
	}
	blocks := restored.List()
	if len(blocks) != 1 || blocks[0].IP != "192.0.2.10" || blocks[0].Status != statusApplied {
		t.Fatalf("restored blocks=%+v", blocks)
	}
}
