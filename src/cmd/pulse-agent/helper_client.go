package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type helperClient struct {
	path string
}

func (h helperClient) Reconcile(ctx context.Context, blocks []firewallBlock) error {
	payload, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", h.path, "reconcile")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper reconcile: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func (h helperClient) Status(ctx context.Context) (any, error) {
	command := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", h.path, "status")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("helper status: %w: %s", err, bytes.TrimSpace(output))
	}
	var result any
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, err
	}
	return result, nil
}
