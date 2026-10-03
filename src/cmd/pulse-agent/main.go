package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9094", "HTTP listen address")
	tokenFile := flag.String("token-file", "", "bearer token file")
	allowedCallers := flag.String("allowed-callers", "127.0.0.1", "comma-separated caller IPs")
	protectedIPs := flag.String("protected-ips", "", "comma-separated IPs that can never be blocked")
	stateFile := flag.String("state-file", "/var/lib/pulse-agent/blocks.json", "persistent state file")
	auditFile := flag.String("audit-file", "/var/lib/pulse-agent/audit.jsonl", "persistent audit log")
	helperPath := flag.String("helper", "/usr/local/libexec/pulse-nft-helper", "root-owned nft helper")
	reconcileInterval := flag.Duration("reconcile-interval", 30*time.Second, "firewall reconciliation interval")
	dryRun := flag.Bool("dry-run", false, "persist and audit desired state without changing the firewall")
	flag.Parse()

	logger := log.New(os.Stdout, "pulse-agent ", log.LstdFlags|log.LUTC)
	token, err := readToken(*tokenFile)
	if err != nil {
		logger.Fatal(err)
	}
	callers, err := parseCallerIPs(*allowedCallers)
	if err != nil {
		logger.Fatal(err)
	}
	protected, err := parseProtectedIPs(*protectedIPs)
	if err != nil {
		logger.Fatal(err)
	}

	agent := newAgent(logger, time.Now)
	var fw firewall = helperClient{path: *helperPath}
	mode := "enforcing"
	if *dryRun {
		fw = noopFirewall{}
		mode = "dry-run"
	}
	agent.setMode(mode)
	agent.setRuntime(newStateStore(*stateFile, *auditFile), fw, protected)
	if err := agent.Restore(); err != nil {
		logger.Printf("initial reconcile error=%v", err)
	}

	server := &http.Server{
		Addr:              *listen,
		Handler:           authenticated(agent.handler(), token, callers),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(*reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := agent.Reconcile(); err != nil {
					logger.Printf("periodic reconcile error=%v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("shutdown error=%v", err)
		}
	}()

	logger.Printf("started listen=%s mode=%s", *listen, mode)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal(err)
	}
}

func readToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("token-file is required")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	token := strings.TrimSpace(string(value))
	if len(token) < 32 {
		return "", fmt.Errorf("token must be at least 32 characters")
	}
	return token, nil
}

func parseCallerIPs(value string) ([]netip.Addr, error) {
	var result []netip.Addr
	for _, raw := range strings.Split(value, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("allowed caller %q: %w", raw, err)
		}
		result = append(result, addr.Unmap())
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one allowed caller is required")
	}
	return result, nil
}
