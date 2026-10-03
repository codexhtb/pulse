package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	pendingStoreVersion = 1
	pendingFrameMagic   = "PWL3"
	pendingFrameHeader  = 16
)

type pendingWALRecord struct {
	Sequence uint64          `json:"s"`
	Op       string          `json:"o"`
	Key      string          `json:"k,omitempty"`
	ID       uint64          `json:"i,omitempty"`
	Pending  *pendingQuery   `json:"p,omitempty"`
	Terminal *recentTerminal `json:"t,omitempty"`
}

type pendingCheckpoint struct {
	Version       int                       `json:"version"`
	Sequence      uint64                    `json:"sequence"`
	NextPendingID uint64                    `json:"next_pending_id"`
	CreatedAt     time.Time                 `json:"created_at"`
	Pending       map[string]pendingQuery   `json:"pending"`
	Recent        map[string]recentTerminal `json:"recent"`
}

type pendingRecovery struct {
	Pending       map[string]pendingQuery
	Recent        map[string]recentTerminal
	NextPendingID uint64
	Sequence      uint64
	Recovered     int
	Errors        uint64
	Truncated     uint64
	Unclean       bool
}

type pendingPersistenceSnapshot struct {
	Enabled          bool       `json:"enabled"`
	WALBytes         int64      `json:"wal_bytes"`
	CheckpointBytes  int64      `json:"checkpoint_bytes"`
	LastCheckpointAt *time.Time `json:"last_checkpoint_at"`
	LastSyncAt       *time.Time `json:"last_sync_at"`
	RecoveredPending uint64     `json:"recovered_pending"`
	RecoveryErrors   uint64     `json:"recovery_errors"`
	WriteErrors      uint64     `json:"write_errors"`
	TruncatedBytes   uint64     `json:"truncated_tail_bytes"`
	PersistenceLagMS int64      `json:"persistence_lag_ms"`
	UncleanRecovery  bool       `json:"unclean_recovery"`
	FlushIntervalMS  int64      `json:"flush_interval_ms"`
	SyncIntervalMS   int64      `json:"sync_interval_ms"`
	CheckpointMS     int64      `json:"checkpoint_interval_ms"`
}

type pendingStore struct {
	dir                string
	walPath            string
	checkpointPath     string
	dirtyPath          string
	flushInterval      time.Duration
	syncInterval       time.Duration
	checkpointInterval time.Duration

	mu             sync.Mutex
	flushMu        sync.Mutex
	file           *os.File
	buffer         []pendingWALRecord
	nextSequence   uint64
	oldestBuffered time.Time
	lastFileSync   time.Time
	closed         bool

	encoder *zstd.Encoder
	decoder *zstd.Decoder
	stop    chan struct{}
	done    chan struct{}

	lastCheckpointNS atomic.Int64
	lastSyncNS       atomic.Int64
	recoveredPending atomic.Uint64
	recoveryErrors   atomic.Uint64
	writeErrors      atomic.Uint64
	truncatedBytes   atomic.Uint64
	uncleanRecovery  bool
}

func openPendingStore(dir string, flushInterval, syncInterval, checkpointInterval time.Duration) (*pendingStore, pendingRecovery, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, pendingRecovery{Pending: make(map[string]pendingQuery), Recent: make(map[string]recentTerminal)}, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, pendingRecovery{}, fmt.Errorf("create pending state directory: %w", err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return nil, pendingRecovery{}, fmt.Errorf("create pending WAL encoder: %w", err)
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		encoder.Close()
		return nil, pendingRecovery{}, fmt.Errorf("create pending WAL decoder: %w", err)
	}
	s := &pendingStore{
		dir: dir, walPath: filepath.Join(dir, "pending.wal"), checkpointPath: filepath.Join(dir, "pending.checkpoint"), dirtyPath: filepath.Join(dir, "unclean.marker"),
		flushInterval: flushInterval, syncInterval: syncInterval, checkpointInterval: checkpointInterval,
		encoder: encoder, decoder: decoder, stop: make(chan struct{}), done: make(chan struct{}),
	}
	recovery, err := s.recover()
	if err != nil {
		encoder.Close()
		decoder.Close()
		return nil, pendingRecovery{}, err
	}
	s.nextSequence = recovery.Sequence
	s.recoveredPending.Store(uint64(recovery.Recovered))
	s.recoveryErrors.Store(recovery.Errors)
	s.truncatedBytes.Store(recovery.Truncated)
	s.uncleanRecovery = recovery.Unclean
	file, err := os.OpenFile(s.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		encoder.Close()
		decoder.Close()
		return nil, pendingRecovery{}, fmt.Errorf("open pending WAL: %w", err)
	}
	s.file = file
	if err := os.WriteFile(s.dirtyPath, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o640); err != nil {
		_ = file.Close()
		encoder.Close()
		decoder.Close()
		return nil, pendingRecovery{}, fmt.Errorf("mark pending state unclean: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		_ = file.Close()
		encoder.Close()
		decoder.Close()
		return nil, pendingRecovery{}, err
	}
	go s.run()
	return s, recovery, nil
}

func (s *pendingStore) appendPut(key string, pending pendingQuery) error {
	copyPending := pending
	return s.appendRecord(pendingWALRecord{Op: "put", Key: key, ID: pending.ID, Pending: &copyPending})
}

func (s *pendingStore) appendDelete(key string, id uint64, terminal recentTerminal) error {
	copyTerminal := terminal
	return s.appendRecord(pendingWALRecord{Op: "delete", Key: key, ID: id, Terminal: &copyTerminal})
}

func (s *pendingStore) appendRecord(record pendingWALRecord) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.writeErrors.Add(1)
		return errors.New("pending store is closed")
	}
	s.nextSequence++
	record.Sequence = s.nextSequence
	s.buffer = append(s.buffer, record)
	if s.oldestBuffered.IsZero() {
		s.oldestBuffered = time.Now().UTC()
	}
	return nil
}

func (s *pendingStore) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.flush(false); err != nil {
				s.writeErrors.Add(1)
			}
		case <-s.stop:
			if err := s.flush(true); err != nil {
				s.writeErrors.Add(1)
			}
			return
		}
	}
}

func (s *pendingStore) flush(forceSync bool) error {
	if s == nil {
		return nil
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		if forceSync && s.file != nil {
			return s.syncFile()
		}
		return nil
	}
	records := append([]pendingWALRecord(nil), s.buffer...)
	s.buffer = s.buffer[:0]
	s.oldestBuffered = time.Time{}
	s.mu.Unlock()

	raw, err := json.Marshal(records)
	if err != nil {
		s.restoreRecords(records)
		return fmt.Errorf("encode pending WAL block: %w", err)
	}
	compressed := s.encoder.EncodeAll(raw, nil)
	frame := makePendingFrame(raw, compressed)
	if _, err := s.file.Write(frame); err != nil {
		s.restoreRecords(records)
		return fmt.Errorf("write pending WAL block: %w", err)
	}
	now := time.Now().UTC()
	if forceSync || s.lastFileSync.IsZero() || now.Sub(s.lastFileSync) >= s.syncInterval {
		if err := s.syncFile(); err != nil {
			return err
		}
	}
	return nil
}

func (s *pendingStore) restoreRecords(records []pendingWALRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := make([]pendingWALRecord, 0, len(records)+len(s.buffer))
	merged = append(merged, records...)
	merged = append(merged, s.buffer...)
	s.buffer = merged
	if s.oldestBuffered.IsZero() {
		s.oldestBuffered = time.Now().UTC()
	}
}

func (s *pendingStore) syncFile() error {
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync pending WAL: %w", err)
	}
	s.lastFileSync = time.Now().UTC()
	s.lastSyncNS.Store(s.lastFileSync.UnixNano())
	return nil
}

func makePendingFrame(raw, compressed []byte) []byte {
	frame := make([]byte, pendingFrameHeader+len(compressed))
	copy(frame[:4], pendingFrameMagic)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(compressed)))
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(raw)))
	binary.BigEndian.PutUint32(frame[12:16], crc32.ChecksumIEEE(compressed))
	copy(frame[pendingFrameHeader:], compressed)
	return frame
}

func (s *pendingStore) prepareCheckpoint() (uint64, error) {
	if s == nil {
		return 0, nil
	}
	if err := s.flush(true); err != nil {
		return 0, err
	}
	s.mu.Lock()
	sequence := s.nextSequence
	s.mu.Unlock()
	if err := s.rotateWAL(sequence); err != nil {
		return 0, err
	}
	return sequence, nil
}

func (s *pendingStore) installCheckpoint(state pendingCheckpoint, sequence uint64) error {
	if s == nil {
		return nil
	}
	state.Sequence = sequence
	state.Version = pendingStoreVersion
	state.CreatedAt = time.Now().UTC()
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode pending checkpoint: %w", err)
	}
	compressed := s.encoder.EncodeAll(raw, nil)
	frame := makePendingFrame(raw, compressed)
	tmp := s.checkpointPath + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create pending checkpoint: %w", err)
	}
	if _, err := file.Write(frame); err != nil {
		_ = file.Close()
		return fmt.Errorf("write pending checkpoint: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync pending checkpoint: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close pending checkpoint: %w", err)
	}
	if err := os.Rename(tmp, s.checkpointPath); err != nil {
		return fmt.Errorf("install pending checkpoint: %w", err)
	}
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	s.lastCheckpointNS.Store(state.CreatedAt.UnixNano())
	entries, _ := filepath.Glob(filepath.Join(s.dir, "pending.wal.*"))
	for _, entry := range entries {
		value, err := strconv.ParseUint(strings.TrimPrefix(filepath.Base(entry), "pending.wal."), 10, 64)
		if err == nil && value <= sequence {
			_ = os.Remove(entry)
		}
	}
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	return nil
}

func (s *pendingStore) rotateWAL(sequence uint64) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close pending WAL for rotation: %w", err)
	}
	archive := filepath.Join(s.dir, "pending.wal."+strconv.FormatUint(sequence, 10))
	if info, err := os.Stat(s.walPath); err == nil && info.Size() > 0 {
		if err := os.Rename(s.walPath, archive); err != nil {
			return fmt.Errorf("rotate pending WAL: %w", err)
		}
	}
	file, err := os.OpenFile(s.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("open rotated pending WAL: %w", err)
	}
	s.file = file
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	return nil
}

func (s *pendingStore) recover() (pendingRecovery, error) {
	recovery := pendingRecovery{Pending: make(map[string]pendingQuery), Recent: make(map[string]recentTerminal)}
	if _, err := os.Stat(s.dirtyPath); err == nil {
		recovery.Unclean = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return recovery, fmt.Errorf("inspect pending dirty marker: %w", err)
	}
	if data, err := os.ReadFile(s.checkpointPath); err == nil {
		var checkpoint pendingCheckpoint
		if err := s.decodeSingleFrame(data, &checkpoint); err != nil {
			return recovery, fmt.Errorf("decode pending checkpoint: %w", err)
		} else if checkpoint.Version != pendingStoreVersion {
			return recovery, fmt.Errorf("unsupported pending checkpoint version %d", checkpoint.Version)
		} else {
			recovery.Pending = checkpoint.Pending
			recovery.Recent = checkpoint.Recent
			recovery.NextPendingID = checkpoint.NextPendingID
			recovery.Sequence = checkpoint.Sequence
			s.lastCheckpointNS.Store(checkpoint.CreatedAt.UnixNano())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return recovery, fmt.Errorf("read pending checkpoint: %w", err)
	}

	segments, err := filepath.Glob(filepath.Join(s.dir, "pending.wal.*"))
	if err != nil {
		return recovery, fmt.Errorf("list pending WAL segments: %w", err)
	}
	sort.Slice(segments, func(i, j int) bool { return walSegmentSequence(segments[i]) < walSegmentSequence(segments[j]) })
	segments = append(segments, s.walPath)
	for _, segment := range segments {
		data, err := os.ReadFile(segment)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return recovery, fmt.Errorf("read pending WAL %s: %w", segment, err)
		}
		valid, decodeErrors := s.replayFrames(data, &recovery)
		if decodeErrors > 0 {
			recovery.Errors += uint64(decodeErrors)
			recovery.Truncated += uint64(len(data) - valid)
			if segment == s.walPath {
				if err := os.Truncate(segment, int64(valid)); err != nil {
					return recovery, fmt.Errorf("truncate invalid pending WAL tail: %w", err)
				}
			}
		}
	}
	now := time.Now().UTC()
	for key, terminal := range recovery.Recent {
		if now.Sub(terminal.At) > recentTerminalTTL {
			delete(recovery.Recent, key)
		}
	}
	for _, pending := range recovery.Pending {
		if pending.ID > recovery.NextPendingID {
			recovery.NextPendingID = pending.ID
		}
	}
	recovery.Recovered = len(recovery.Pending)
	return recovery, nil
}

func walSegmentSequence(path string) uint64 {
	value, _ := strconv.ParseUint(strings.TrimPrefix(filepath.Base(path), "pending.wal."), 10, 64)
	return value
}

func (s *pendingStore) replayFrames(data []byte, recovery *pendingRecovery) (int, int) {
	offset, failures := 0, 0
	for offset < len(data) {
		if len(data)-offset < pendingFrameHeader || string(data[offset:offset+4]) != pendingFrameMagic {
			failures++
			break
		}
		compressedLength := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		rawLength := int(binary.BigEndian.Uint32(data[offset+8 : offset+12]))
		checksum := binary.BigEndian.Uint32(data[offset+12 : offset+16])
		end := offset + pendingFrameHeader + compressedLength
		if compressedLength <= 0 || rawLength <= 0 || end > len(data) {
			failures++
			break
		}
		compressed := data[offset+pendingFrameHeader : end]
		if crc32.ChecksumIEEE(compressed) != checksum {
			failures++
			break
		}
		raw, err := s.decoder.DecodeAll(compressed, nil)
		if err != nil || len(raw) != rawLength {
			failures++
			break
		}
		var records []pendingWALRecord
		if err := json.Unmarshal(raw, &records); err != nil {
			failures++
			break
		}
		for _, record := range records {
			if record.Sequence <= recovery.Sequence {
				continue
			}
			applyPendingRecord(recovery, record)
			recovery.Sequence = record.Sequence
		}
		offset = end
	}
	return offset, failures
}

func applyPendingRecord(recovery *pendingRecovery, record pendingWALRecord) {
	switch record.Op {
	case "put":
		if record.Pending != nil {
			recovery.Pending[record.Key] = *record.Pending
			delete(recovery.Recent, record.Key)
			if record.ID > recovery.NextPendingID {
				recovery.NextPendingID = record.ID
			}
		}
	case "delete":
		if pending, ok := recovery.Pending[record.Key]; ok && pending.ID == record.ID {
			delete(recovery.Pending, record.Key)
		}
		if record.Terminal != nil {
			recovery.Recent[record.Key] = *record.Terminal
		}
	}
}

func (s *pendingStore) decodeSingleFrame(data []byte, value any) error {
	if len(data) < pendingFrameHeader || string(data[:4]) != pendingFrameMagic {
		return errors.New("invalid pending frame header")
	}
	compressedLength := int(binary.BigEndian.Uint32(data[4:8]))
	rawLength := int(binary.BigEndian.Uint32(data[8:12]))
	if compressedLength <= 0 || rawLength <= 0 || pendingFrameHeader+compressedLength != len(data) {
		return errors.New("invalid pending frame length")
	}
	compressed := data[pendingFrameHeader:]
	if crc32.ChecksumIEEE(compressed) != binary.BigEndian.Uint32(data[12:16]) {
		return errors.New("invalid pending frame checksum")
	}
	raw, err := s.decoder.DecodeAll(compressed, nil)
	if err != nil {
		return err
	}
	if len(raw) != rawLength {
		return errors.New("invalid pending uncompressed length")
	}
	return json.NewDecoder(bytes.NewReader(raw)).Decode(value)
}

func (s *pendingStore) close(clean bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	<-s.done
	s.flushMu.Lock()
	err := s.file.Close()
	s.flushMu.Unlock()
	if clean {
		if removeErr := os.Remove(s.dirtyPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = removeErr
		}
		if syncErr := syncDirectory(s.dir); syncErr != nil && err == nil {
			err = syncErr
		}
	}
	s.encoder.Close()
	s.decoder.Close()
	return err
}

func (s *pendingStore) snapshot(now time.Time) pendingPersistenceSnapshot {
	if s == nil {
		return pendingPersistenceSnapshot{}
	}
	s.mu.Lock()
	oldest := s.oldestBuffered
	s.mu.Unlock()
	lag := int64(0)
	if !oldest.IsZero() {
		lag = now.Sub(oldest).Milliseconds()
		if lag < 0 {
			lag = 0
		}
	}
	walBytes := int64(0)
	entries, _ := filepath.Glob(filepath.Join(s.dir, "pending.wal*"))
	for _, entry := range entries {
		if info, err := os.Stat(entry); err == nil {
			walBytes += info.Size()
		}
	}
	checkpointBytes := int64(0)
	if info, err := os.Stat(s.checkpointPath); err == nil {
		checkpointBytes = info.Size()
	}
	return pendingPersistenceSnapshot{
		Enabled: true, WALBytes: walBytes, CheckpointBytes: checkpointBytes,
		LastCheckpointAt: timeFromUnixNano(s.lastCheckpointNS.Load()), LastSyncAt: timeFromUnixNano(s.lastSyncNS.Load()),
		RecoveredPending: s.recoveredPending.Load(), RecoveryErrors: s.recoveryErrors.Load(), WriteErrors: s.writeErrors.Load(), TruncatedBytes: s.truncatedBytes.Load(),
		PersistenceLagMS: lag, UncleanRecovery: s.uncleanRecovery,
		FlushIntervalMS: s.flushInterval.Milliseconds(), SyncIntervalMS: s.syncInterval.Milliseconds(), CheckpointMS: s.checkpointInterval.Milliseconds(),
	}
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open pending state directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync pending state directory: %w", err)
	}
	return nil
}

func copyPendingMap(input map[string]pendingQuery) map[string]pendingQuery {
	result := make(map[string]pendingQuery, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func copyRecentMap(input map[string]recentTerminal) map[string]recentTerminal {
	result := make(map[string]recentTerminal, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
