package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const controlStateVersion = 1

type nodeApplyResult struct {
	NodeID      string     `json:"node_id"`
	Action      string     `json:"action"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastError   string     `json:"last_error,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	AgentResult string     `json:"agent_result,omitempty"`
}

type desiredDNSState struct {
	IP        string                     `json:"ip"`
	Desired   string                     `json:"desired"`
	CreatedAt time.Time                  `json:"created_at"`
	UpdatedAt time.Time                  `json:"updated_at"`
	ExpiresAt *time.Time                 `json:"expires_at"`
	Duration  string                     `json:"duration"`
	Reason    string                     `json:"reason"`
	Nodes     map[string]nodeApplyResult `json:"nodes"`
}

type durableControlState struct {
	Version int                        `json:"version"`
	Records map[string]desiredDNSState `json:"records"`
}

type controlStateStore struct {
	path string
}

func cloneControlState(state durableControlState) durableControlState {
	clone := durableControlState{Version: state.Version, Records: make(map[string]desiredDNSState, len(state.Records))}
	for ip, record := range state.Records {
		record.Nodes = make(map[string]nodeApplyResult, len(record.Nodes))
		for nodeID, result := range state.Records[ip].Nodes {
			record.Nodes[nodeID] = result
		}
		clone.Records[ip] = record
	}
	return clone
}

func newControlStateStore(path string) *controlStateStore {
	return &controlStateStore{path: path}
}

func (s *controlStateStore) load() (durableControlState, error) {
	state := durableControlState{Version: controlStateVersion, Records: make(map[string]desiredDNSState)}
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<20))
	if err := decoder.Decode(&state); err != nil {
		return state, err
	}
	if state.Version != controlStateVersion {
		return state, errors.New("unsupported DNS control state version")
	}
	if state.Records == nil {
		state.Records = make(map[string]desiredDNSState)
	}
	return state, nil
}

func (s *controlStateStore) save(state durableControlState) error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".dns-control-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
