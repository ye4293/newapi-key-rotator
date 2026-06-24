package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// poolState is the persisted shape of the key pool. It lives in <DataDir>/pool.json
// so that submitting keys via the web console survives container restarts without
// touching any file by hand.
type poolState struct {
	Keys        []string `json:"keys"`        // ordered backup keys (no duplicates, no blanks)
	Index       int      `json:"index"`       // next key to apply on the next auto-disable
	Fingerprint string   `json:"fingerprint"` // sha256 prefix of the key set, used to detect replacement
	Exhausted   bool     `json:"exhausted"`   // true once every key has been tried and the pool ran out
}

// Store guards the key pool and its rotation progress. Both the HTTP console and
// the background rotation loop touch it, so every access goes through the mutex.
type Store struct {
	mu   sync.Mutex
	path string
	st   poolState
}

// PoolSnapshot is a read-only view returned to the web console.
type PoolSnapshot struct {
	Total      int    `json:"total"`
	Index      int    `json:"index"`
	Remaining  int    `json:"remaining"`
	Exhausted  bool   `json:"exhausted"`
	CurrentKey string `json:"current_key"` // masked preview of the last-applied key, or ""
}

// NewStore loads the persisted pool from disk if present, otherwise starts empty.
func NewStore(poolPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(poolPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %q: %w", filepath.Dir(poolPath), err)
	}
	s := &Store{path: poolPath}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read pool file: %w", err)
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		return nil, fmt.Errorf("parse pool file %q: %w", s.path, err)
	}
	return s, nil
}

// SetKeys replaces the entire pool with a fresh batch parsed from a newline-separated
// blob. Order is preserved, blanks and duplicates are dropped. Progress resets to the
// start so the new batch is consumed from its first key. Returns the number of keys kept.
func (s *Store) SetKeys(raw string) (int, error) {
	keys := parseKeys(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Keys = keys
	s.st.Index = 0
	s.st.Exhausted = false
	s.st.Fingerprint = fingerprint(keys)
	if err := s.persistLocked(); err != nil {
		return 0, err
	}
	return len(keys), nil
}

// AppendKeys adds new keys to the existing pool, deduplicating against what's
// already there. Progress index and exhausted flag are preserved. Returns the
// number of keys actually added.
func (s *Store) AppendKeys(raw string) (int, error) {
	incoming := parseKeys(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	existing := make(map[string]struct{}, len(s.st.Keys))
	for _, k := range s.st.Keys {
		existing[k] = struct{}{}
	}
	added := 0
	for _, k := range incoming {
		if _, dup := existing[k]; dup {
			continue
		}
		s.st.Keys = append(s.st.Keys, k)
		existing[k] = struct{}{}
		added++
	}
	if added == 0 {
		return 0, nil
	}
	s.st.Exhausted = false
	s.st.Fingerprint = fingerprint(s.st.Keys)
	if err := s.persistLocked(); err != nil {
		return 0, err
	}
	return added, nil
}

// PeekNext returns the next key to apply without advancing. ok is false when the
// pool is empty or already exhausted.
func (s *Store) PeekNext() (key string, index int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Index < 0 || s.st.Index >= len(s.st.Keys) {
		return "", s.st.Index, false
	}
	return s.st.Keys[s.st.Index], s.st.Index, true
}

// CommitAdvance moves to the next key after a successful apply and persists progress.
// It marks the pool exhausted once the last key has been consumed.
func (s *Store) CommitAdvance() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Index++
	if s.st.Index >= len(s.st.Keys) {
		s.st.Exhausted = true
	}
	return s.persistLocked()
}

// Snapshot returns a masked, read-only view for the console.
func (s *Store) Snapshot() PoolSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := len(s.st.Keys)
	remaining := total - s.st.Index
	if remaining < 0 {
		remaining = 0
	}
	current := ""
	// The most recently applied key sits at Index-1.
	if s.st.Index > 0 && s.st.Index-1 < total {
		current = maskKey(s.st.Keys[s.st.Index-1])
	}
	return PoolSnapshot{
		Total:      total,
		Index:      s.st.Index,
		Remaining:  remaining,
		Exhausted:  s.st.Exhausted,
		CurrentKey: current,
	}
}

// persistLocked writes the state atomically. Caller must hold the mutex.
func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write pool file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit pool file: %w", err)
	}
	return nil
}

func parseKeys(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		k := strings.TrimSpace(line)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	return keys
}

func fingerprint(keys []string) string {
	h := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(h[:])[:12]
}

// maskKey reveals only the last 4 characters so the console never exposes secrets.
func maskKey(k string) string {
	if len(k) <= 4 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}
