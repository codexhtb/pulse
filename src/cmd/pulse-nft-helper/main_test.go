package main

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

const nft109Ruleset = `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak #3","json_schema_version":1}},{"table":{"family":"inet","name":"pulse","handle":1}},{"set":{"family":"inet","name":"blocked_dns_v4","table":"pulse","type":"ipv4_addr","handle":2,"flags":["timeout"],"elem":[{"elem":{"val":"192.0.2.2","timeout":3600,"expires":3599}}]}},{"chain":{"family":"inet","table":"pulse","name":"input","handle":1,"type":"filter","hook":"input","prio":0,"policy":"accept"}},{"rule":{"family":"inet","table":"pulse","chain":"input","handle":3,"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"@blocked_dns_v4"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"198.51.100.53"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":53}},{"counter":{"packets":0,"bytes":0}},{"drop":null}]}},{"rule":{"family":"inet","table":"pulse","chain":"input","handle":4,"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"@blocked_dns_v4"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"198.51.100.53"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":53}},{"counter":{"packets":0,"bytes":0}},{"drop":null}]}}]}`

func TestValidateConfigRequiresLocalDNSIP(t *testing.T) {
	config := helperConfig{DNSIP: "198.51.100.53", ProtectedIPs: []string{"192.0.2.10", "192.0.2.1"}}
	validated, err := validateConfig(config, []netip.Addr{netip.MustParseAddr("192.0.2.53"), netip.MustParseAddr("198.51.100.53")})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"192.0.2.53", "198.51.100.53", "192.0.2.10", "192.0.2.1"} {
		if _, ok := validated.protected[netip.MustParseAddr(value)]; !ok {
			t.Fatalf("%s is not protected", value)
		}
	}
	if _, err := validateConfig(config, []netip.Addr{netip.MustParseAddr("203.0.113.1")}); err == nil {
		t.Fatal("non-local DNS IP accepted")
	}
}

func TestValidateBlocksRejectsUnsafeAndProtectedAddresses(t *testing.T) {
	now := time.Now().UTC()
	protected := map[netip.Addr]struct{}{netip.MustParseAddr("192.0.2.10"): {}}
	for _, ip := range []string{"127.0.0.1", "0.0.0.0", "224.0.0.1", "169.254.1.1", "192.0.2.10", "192.0.2.0/24", "2001:db8::1"} {
		if _, err := validateBlocks([]firewallBlock{{IP: ip}}, protected, now); err == nil {
			t.Fatalf("unsafe IP %s accepted", ip)
		}
	}
}

func TestValidateBlocksUnmapsIPv4MappedIPv6(t *testing.T) {
	blocks, err := validateBlocks([]firewallBlock{{IP: "::ffff:192.0.2.2"}}, map[netip.Addr]struct{}{}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].IP != "192.0.2.2" {
		t.Fatalf("unexpected blocks: %+v", blocks)
	}
}

func TestBuildRulesetScopesDropsToConfiguredDNS(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	script := string(buildRuleset(netip.MustParseAddr("198.51.100.53"), []firewallBlock{{IP: "192.0.2.2", ExpiresAt: &expires}, {IP: "198.51.100.10"}}, true, now))
	for _, expected := range []string{
		"delete table inet pulse",
		"set blocked_dns_v4",
		"type ipv4_addr",
		"flags timeout",
		"192.0.2.2 timeout 3600s",
		"198.51.100.10",
		"ip daddr 198.51.100.53 udp dport 53",
		"ip daddr 198.51.100.53 tcp dport 53",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("ruleset missing %q:\n%s", expected, script)
		}
	}
	if strings.Contains(script, "dport 22") || strings.Contains(script, "policy drop") {
		t.Fatalf("ruleset affects unrelated traffic:\n%s", script)
	}
}

func TestValidateRulesetAcceptsNft109NumericPriority(t *testing.T) {
	if err := validateRuleset([]byte(nft109Ruleset), netip.MustParseAddr("198.51.100.53")); err != nil {
		t.Fatalf("nftables 1.0.9 JSON rejected: %v", err)
	}
}

func TestValidateRulesetAcceptsSymbolicFilterPriority(t *testing.T) {
	ruleset := strings.Replace(nft109Ruleset, `"prio":0`, `"prio":"filter"`, 1)
	if err := validateRuleset([]byte(ruleset), netip.MustParseAddr("198.51.100.53")); err != nil {
		t.Fatalf("symbolic filter priority rejected: %v", err)
	}
}

func TestValidateRulesetRejectsChangedFirewallSemantics(t *testing.T) {
	for name, ruleset := range map[string]string{
		"wrong destination": strings.Replace(nft109Ruleset, "198.51.100.53", "198.51.100.54", 1),
		"wrong policy":      strings.Replace(nft109Ruleset, `"policy":"accept"`, `"policy":"drop"`, 1),
		"extra set flag":    strings.Replace(nft109Ruleset, `"flags":["timeout"]`, `"flags":["timeout","interval"]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRuleset([]byte(ruleset), netip.MustParseAddr("198.51.100.53")); err == nil {
				t.Fatal("changed firewall shape accepted")
			}
		})
	}
}
