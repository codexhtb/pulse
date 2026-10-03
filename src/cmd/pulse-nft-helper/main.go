package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	configPath = "/etc/pulse-agent/helper.json"
	nftPath    = "/usr/sbin/nft"
)

type helperConfig struct {
	DNSIP        string   `json:"dns_ip"`
	ProtectedIPs []string `json:"protected_ips"`
}

type firewallBlock struct {
	IP        string     `json:"ip"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type reconcileRequest struct {
	Blocks []firewallBlock `json:"blocks"`
}

type validatedConfig struct {
	dnsIP     netip.Addr
	protected map[netip.Addr]struct{}
}

func main() {
	log.SetPrefix("pulse-nft-helper ")
	log.SetFlags(log.LstdFlags | log.LUTC)
	if os.Geteuid() != 0 {
		log.Fatal("must run as root")
	}
	if len(os.Args) != 2 || (os.Args[1] != "reconcile" && os.Args[1] != "status") {
		log.Fatal("usage: pulse-nft-helper reconcile|status")
	}
	config, err := loadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}
	local, err := localAddresses()
	if err != nil {
		log.Fatal(err)
	}
	validated, err := validateConfig(config, local)
	if err != nil {
		log.Fatal(err)
	}

	switch os.Args[1] {
	case "reconcile":
		request, err := decodeRequest(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		blocks, err := validateBlocks(request.Blocks, validated.protected, time.Now().UTC())
		if err != nil {
			log.Fatal(err)
		}
		exists := tableExists()
		script := buildRuleset(validated.dnsIP, blocks, exists, time.Now().UTC())
		if _, err := runNft([]string{"-c", "-f", "-"}, script); err != nil {
			log.Fatalf("validate ruleset: %v", err)
		}
		if _, err := runNft([]string{"-f", "-"}, script); err != nil {
			log.Fatalf("apply ruleset: %v", err)
		}
		log.Printf("action=reconcile dns_ip=%s blocks=%d result=applied", validated.dnsIP, len(blocks))
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "applied", "count": len(blocks)})
	case "status":
		output, err := runNft([]string{"-j", "list", "table", "inet", "pulse"}, nil)
		if err != nil {
			log.Fatalf("read pulse table: %v", err)
		}
		if err := validateRuleset(output, validated.dnsIP); err != nil {
			log.Fatalf("validate pulse table: %v", err)
		}
		_, _ = os.Stdout.Write(output)
	}
}

type nftRuleset struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

type nftTable struct {
	Family string `json:"family"`
	Name   string `json:"name"`
}

type nftSet struct {
	Family string   `json:"family"`
	Table  string   `json:"table"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Flags  []string `json:"flags"`
}

type nftChain struct {
	Family   string          `json:"family"`
	Table    string          `json:"table"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Hook     string          `json:"hook"`
	Priority json.RawMessage `json:"prio"`
	Policy   string          `json:"policy"`
}

type nftRule struct {
	Family string                       `json:"family"`
	Table  string                       `json:"table"`
	Chain  string                       `json:"chain"`
	Expr   []map[string]json.RawMessage `json:"expr"`
}

type nftMatch struct {
	Op    string          `json:"op"`
	Left  json.RawMessage `json:"left"`
	Right json.RawMessage `json:"right"`
}

type nftPayloadOperand struct {
	Payload struct {
		Protocol string `json:"protocol"`
		Field    string `json:"field"`
	} `json:"payload"`
}

func validateRuleset(data []byte, dnsIP netip.Addr) error {
	var ruleset nftRuleset
	if err := json.Unmarshal(data, &ruleset); err != nil {
		return fmt.Errorf("decode nft JSON: %w", err)
	}
	if len(ruleset.Nftables) == 0 {
		return errors.New("nft JSON has no objects")
	}

	var tableCount, setCount, chainCount int
	ruleProtocols := make(map[string]bool)
	for _, object := range ruleset.Nftables {
		if len(object) != 1 {
			return errors.New("nft JSON object has an unexpected shape")
		}
		for kind, raw := range object {
			switch kind {
			case "metainfo":
				continue
			case "table":
				var table nftTable
				if err := json.Unmarshal(raw, &table); err != nil {
					return fmt.Errorf("decode table: %w", err)
				}
				if table.Family != "inet" || table.Name != "pulse" {
					return fmt.Errorf("unexpected table %s %s", table.Family, table.Name)
				}
				tableCount++
			case "set":
				var set nftSet
				if err := json.Unmarshal(raw, &set); err != nil {
					return fmt.Errorf("decode set: %w", err)
				}
				if set.Family != "inet" || set.Table != "pulse" || set.Name != "blocked_dns_v4" || set.Type != "ipv4_addr" ||
					len(set.Flags) != 1 || set.Flags[0] != "timeout" {
					return errors.New("blocked_dns_v4 has an unexpected definition")
				}
				setCount++
			case "chain":
				var chain nftChain
				if err := json.Unmarshal(raw, &chain); err != nil {
					return fmt.Errorf("decode chain: %w", err)
				}
				if chain.Family != "inet" || chain.Table != "pulse" || chain.Name != "input" || chain.Type != "filter" ||
					chain.Hook != "input" || chain.Policy != "accept" || !filterPriority(chain.Priority) {
					return errors.New("input chain has an unexpected definition")
				}
				chainCount++
			case "rule":
				var rule nftRule
				if err := json.Unmarshal(raw, &rule); err != nil {
					return fmt.Errorf("decode rule: %w", err)
				}
				protocol, err := validateDNSDropRule(rule, dnsIP)
				if err != nil {
					return err
				}
				if ruleProtocols[protocol] {
					return fmt.Errorf("duplicate %s DNS drop rule", protocol)
				}
				ruleProtocols[protocol] = true
			default:
				return fmt.Errorf("unexpected nft JSON object %q", kind)
			}
		}
	}

	if tableCount != 1 || setCount != 1 || chainCount != 1 || len(ruleProtocols) != 2 || !ruleProtocols["udp"] || !ruleProtocols["tcp"] {
		return fmt.Errorf("unexpected object counts: tables=%d sets=%d chains=%d rules=%d", tableCount, setCount, chainCount, len(ruleProtocols))
	}
	return nil
}

func filterPriority(raw json.RawMessage) bool {
	var symbolic string
	if json.Unmarshal(raw, &symbolic) == nil {
		return symbolic == "filter"
	}
	var numeric json.Number
	if json.Unmarshal(raw, &numeric) == nil {
		return numeric.String() == "0"
	}
	return false
}

func validateDNSDropRule(rule nftRule, dnsIP netip.Addr) (string, error) {
	if rule.Family != "inet" || rule.Table != "pulse" || rule.Chain != "input" || len(rule.Expr) != 5 {
		return "", errors.New("DNS drop rule has an unexpected definition")
	}

	var sourceSet, destination, destinationPort, counter, drop bool
	var protocol string
	for _, expression := range rule.Expr {
		if len(expression) != 1 {
			return "", errors.New("DNS drop rule expression has an unexpected shape")
		}
		for kind, raw := range expression {
			switch kind {
			case "match":
				var match nftMatch
				var left nftPayloadOperand
				if json.Unmarshal(raw, &match) != nil || match.Op != "==" || json.Unmarshal(match.Left, &left) != nil {
					return "", errors.New("DNS drop rule has an invalid match")
				}
				switch {
				case left.Payload.Protocol == "ip" && left.Payload.Field == "saddr" && rawStringEquals(match.Right, "@blocked_dns_v4"):
					sourceSet = true
				case left.Payload.Protocol == "ip" && left.Payload.Field == "daddr" && rawStringEquals(match.Right, dnsIP.String()):
					destination = true
				case (left.Payload.Protocol == "udp" || left.Payload.Protocol == "tcp") && left.Payload.Field == "dport" && rawNumberEquals(match.Right, "53"):
					protocol = left.Payload.Protocol
					destinationPort = true
				default:
					return "", errors.New("DNS drop rule has an unexpected match")
				}
			case "counter":
				var value map[string]json.RawMessage
				if json.Unmarshal(raw, &value) != nil {
					return "", errors.New("DNS drop rule has an invalid counter")
				}
				counter = true
			case "drop":
				if string(raw) != "null" {
					return "", errors.New("DNS drop rule has an invalid drop verdict")
				}
				drop = true
			default:
				return "", fmt.Errorf("DNS drop rule has an unexpected expression %q", kind)
			}
		}
	}
	if !sourceSet || !destination || !destinationPort || !counter || !drop || protocol == "" {
		return "", errors.New("DNS drop rule is incomplete")
	}
	return protocol, nil
}

func rawStringEquals(raw json.RawMessage, expected string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == expected
}

func rawNumberEquals(raw json.RawMessage, expected string) bool {
	var value json.Number
	return json.Unmarshal(raw, &value) == nil && value.String() == expected
}

func loadConfig(path string) (helperConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return helperConfig{}, err
	}
	defer file.Close()
	var config helperConfig
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return helperConfig{}, err
	}
	return config, nil
}

func validateConfig(config helperConfig, local []netip.Addr) (validatedConfig, error) {
	dnsIP, err := parseIPv4(config.DNSIP)
	if err != nil {
		return validatedConfig{}, fmt.Errorf("dns_ip: %w", err)
	}
	protected := make(map[netip.Addr]struct{})
	localDNS := false
	for _, addr := range local {
		addr = addr.Unmap()
		if addr.Is4() {
			protected[addr] = struct{}{}
		}
		if addr == dnsIP {
			localDNS = true
		}
	}
	if !localDNS {
		return validatedConfig{}, errors.New("dns_ip is not assigned to this server")
	}
	for _, raw := range config.ProtectedIPs {
		addr, err := parseIPv4(raw)
		if err != nil {
			return validatedConfig{}, fmt.Errorf("protected IP %q: %w", raw, err)
		}
		protected[addr] = struct{}{}
	}
	return validatedConfig{dnsIP: dnsIP, protected: protected}, nil
}

func validateBlocks(blocks []firewallBlock, protected map[netip.Addr]struct{}, now time.Time) ([]firewallBlock, error) {
	if len(blocks) > 10_000 {
		return nil, errors.New("too many blocks")
	}
	seen := make(map[netip.Addr]struct{}, len(blocks))
	result := make([]firewallBlock, 0, len(blocks))
	for _, block := range blocks {
		addr, err := parseIPv4(block.IP)
		if err != nil {
			return nil, fmt.Errorf("block IP %q: %w", block.IP, err)
		}
		if unsafeAddress(addr) {
			return nil, fmt.Errorf("block IP %q is unsafe", block.IP)
		}
		if _, denied := protected[addr]; denied {
			return nil, fmt.Errorf("block IP %q is protected", block.IP)
		}
		if _, duplicate := seen[addr]; duplicate {
			return nil, fmt.Errorf("duplicate block IP %q", block.IP)
		}
		seen[addr] = struct{}{}
		if block.ExpiresAt != nil && !block.ExpiresAt.After(now) {
			continue
		}
		block.IP = addr.String()
		result = append(result, block)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].IP < result[j].IP })
	return result, nil
}

func parseIPv4(raw string) (netip.Addr, error) {
	value := strings.TrimSpace(raw)
	if strings.Contains(value, "/") {
		return netip.Addr{}, errors.New("CIDR prefixes are not allowed")
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, errors.New("invalid IP address")
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return netip.Addr{}, errors.New("only IPv4 addresses are supported")
	}
	return addr, nil
}

func unsafeAddress(addr netip.Addr) bool {
	return addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr == netip.MustParseAddr("255.255.255.255")
}

func buildRuleset(dnsIP netip.Addr, blocks []firewallBlock, replace bool, now time.Time) []byte {
	var script strings.Builder
	if replace {
		script.WriteString("delete table inet pulse\n")
	}
	script.WriteString("table inet pulse {\n")
	script.WriteString("  set blocked_dns_v4 {\n")
	script.WriteString("    type ipv4_addr\n")
	script.WriteString("    flags timeout\n")
	if len(blocks) > 0 {
		script.WriteString("    elements = { ")
		for index, block := range blocks {
			if index > 0 {
				script.WriteString(", ")
			}
			script.WriteString(block.IP)
			if block.ExpiresAt != nil {
				seconds := int64(math.Ceil(block.ExpiresAt.Sub(now).Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				fmt.Fprintf(&script, " timeout %ds", seconds)
			}
		}
		script.WriteString(" }\n")
	}
	script.WriteString("  }\n")
	script.WriteString("  chain input {\n")
	script.WriteString("    type filter hook input priority filter; policy accept;\n")
	fmt.Fprintf(&script, "    ip saddr @blocked_dns_v4 ip daddr %s udp dport 53 counter drop\n", dnsIP)
	fmt.Fprintf(&script, "    ip saddr @blocked_dns_v4 ip daddr %s tcp dport 53 counter drop\n", dnsIP)
	script.WriteString("  }\n")
	script.WriteString("}\n")
	return []byte(script.String())
}

func decodeRequest(reader io.Reader) (reconcileRequest, error) {
	var request reconcileRequest
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return request, errors.New("input must contain one JSON object")
	}
	return request, nil
}

func localAddresses() ([]netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []netip.Addr
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err == nil {
				result = append(result, prefix.Addr())
			}
		}
	}
	return result, nil
}

func tableExists() bool {
	command := exec.Command(nftPath, "list", "table", "inet", "pulse")
	return command.Run() == nil
}

func runNft(arguments []string, input []byte) ([]byte, error) {
	command := exec.Command(nftPath, arguments...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nft %s: %w: %s", strings.Join(arguments, " "), err, bytes.TrimSpace(output))
	}
	return output, nil
}
