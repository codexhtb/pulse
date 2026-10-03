package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type auditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	IP        string    `json:"ip"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	Duration  string    `json:"duration,omitempty"`
	Result    string    `json:"result"`
}

type stateStore struct {
	statePath string
	auditPath string
}

func newStateStore(statePath, auditPath string) *stateStore {
	return &stateStore{statePath: statePath, auditPath: auditPath}
}

func (s *stateStore) load() ([]blockRecord, error) {
	file, err := os.Open(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []blockRecord
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	if err := decoder.Decode(&records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *stateStore) save(records []blockRecord) error {
	dir := filepath.Dir(s.statePath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".blocks-*.json")
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
	if err := encoder.Encode(records); err != nil {
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
	if err := os.Rename(tempPath, s.statePath); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *stateStore) appendAudit(entry auditEntry) error {
	dir := filepath.Dir(s.auditPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(s.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(entry); err != nil {
		return err
	}
	return file.Sync()
}
