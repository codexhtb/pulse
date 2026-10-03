package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var controlNodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type controlNodesFile struct {
	Nodes []controlNodeConfig `json:"nodes"`
}

type controlNodeConfig struct {
	ID             string `json:"id"`
	DisplayName    string `json:"display_name"`
	SourceIdentity string `json:"source_identity"`
	AgentURL       string `json:"agent_url"`
	TokenFile      string `json:"token_file"`
	DNSServiceIP   string `json:"dns_service_ip"`
	ControlEnabled bool   `json:"control_enabled"`
}

type controlNode struct {
	config controlNodeConfig
	client *agentClient
}

func loadControlNodes(path string) ([]*controlNode, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open control nodes config: %w", err)
	}
	defer file.Close()
	var config controlNodesFile
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode control nodes config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("control nodes config must contain one JSON object")
	}

	ids := make(map[string]struct{}, len(config.Nodes))
	identities := make(map[string]string, len(config.Nodes))
	tokenPaths := make(map[string]string, len(config.Nodes))
	tokens := make(map[string]string, len(config.Nodes))
	nodes := make([]*controlNode, 0, len(config.Nodes))
	for index := range config.Nodes {
		node := config.Nodes[index]
		node.ID = strings.TrimSpace(node.ID)
		node.DisplayName = strings.TrimSpace(node.DisplayName)
		node.SourceIdentity = strings.TrimSpace(node.SourceIdentity)
		node.AgentURL = strings.TrimRight(strings.TrimSpace(node.AgentURL), "/")
		node.TokenFile = strings.TrimSpace(node.TokenFile)
		node.DNSServiceIP = strings.TrimSpace(node.DNSServiceIP)
		if !controlNodeIDPattern.MatchString(node.ID) {
			return nil, fmt.Errorf("node %d has invalid id %q", index, node.ID)
		}
		if _, exists := ids[node.ID]; exists {
			return nil, fmt.Errorf("duplicate control node id %q", node.ID)
		}
		ids[node.ID] = struct{}{}
		if node.DisplayName == "" {
			node.DisplayName = node.ID
		}
		if node.SourceIdentity == "" {
			return nil, fmt.Errorf("node %q source_identity is required", node.ID)
		}
		if other, exists := identities[node.SourceIdentity]; exists {
			return nil, fmt.Errorf("nodes %q and %q share source_identity %q", other, node.ID, node.SourceIdentity)
		}
		identities[node.SourceIdentity] = node.ID
		parsedURL, err := url.Parse(node.AgentURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
			return nil, fmt.Errorf("node %q has invalid agent_url", node.ID)
		}
		if node.TokenFile == "" {
			return nil, fmt.Errorf("node %q token_file is required", node.ID)
		}
		if other, exists := tokenPaths[node.TokenFile]; exists {
			return nil, fmt.Errorf("nodes %q and %q share token_file", other, node.ID)
		}
		tokenPaths[node.TokenFile] = node.ID
		tokenBytes, err := os.ReadFile(node.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("read token for node %q: %w", node.ID, err)
		}
		token := strings.TrimSpace(string(tokenBytes))
		if len(token) < 32 {
			return nil, fmt.Errorf("token for node %q must be at least 32 characters", node.ID)
		}
		if other, exists := tokens[token]; exists {
			return nil, fmt.Errorf("nodes %q and %q share the same bearer secret", other, node.ID)
		}
		tokens[token] = node.ID
		if strings.Contains(node.DNSServiceIP, "/") {
			return nil, fmt.Errorf("node %q dns_service_ip must not be a CIDR", node.ID)
		}
		address, err := netip.ParseAddr(node.DNSServiceIP)
		if err != nil || !address.Unmap().Is4() {
			return nil, fmt.Errorf("node %q has invalid IPv4 dns_service_ip", node.ID)
		}
		node.DNSServiceIP = address.Unmap().String()
		nodes = append(nodes, &controlNode{
			config: node,
			client: &agentClient{baseURL: node.AgentURL, token: token, client: newAgentHTTPClient()},
		})
	}
	return nodes, nil
}
