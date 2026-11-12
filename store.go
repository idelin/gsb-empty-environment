package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
)

// CapabilityStore durably records the highest protocol capability ever
// confirmed by a completed handshake. The record is monotonic: it never
// decreases, so a restart or power loss cannot roll the capability back.
type CapabilityStore struct {
	mu      sync.Mutex
	path    string
	highest Protocol
}

type storeFile struct {
	Highest string `json:"highest"`
}

// OpenCapabilityStore loads the persisted record from path. A missing file
// yields an empty store; a corrupt or unreadable file is a recovery failure
// and is reported as an error so the caller can fail closed.
func OpenCapabilityStore(path string) (*CapabilityStore, error) {
	s := &CapabilityStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("netsec: recover capability store: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("netsec: recover capability store: %w", err)
	}
	s.highest = Protocol(f.Highest)
	return s, nil
}

// Highest returns the highest confirmed capability, or "" if none.
func (s *CapabilityStore) Highest() Protocol {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.highest
}

// Confirm records p as an authenticated capability. It is monotonic: a lower
// or equal protocol is ignored, and the record is persisted before the
// in-memory state advances.
func (s *CapabilityStore) Confirm(p Protocol) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if compareProtocols(p, s.highest) <= 0 {
		return nil
	}
	if err := s.persistLocked(p); err != nil {
		return err
	}
	s.highest = p
	return nil
}

// HasConfirmedAbove reports whether any protocol ranking above p was ever
// confirmed. Used to veto legacy fallback once a newer capability is on
// record.
func (s *CapabilityStore) HasConfirmedAbove(p Protocol) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.highest == "" {
		return false
	}
	return compareProtocols(s.highest, p) > 0
}

// persistLocked writes the record atomically: temp file, fsync, rename. A
// crash between steps leaves the previous record intact.
func (s *CapabilityStore) persistLocked(p Protocol) error {
	data, err := json.Marshal(storeFile{Highest: string(p)})
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("netsec: persist capability store: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("netsec: persist capability store: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("netsec: persist capability store: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("netsec: persist capability store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("netsec: persist capability store: %w", err)
	}
	return nil
}

// compareProtocols orders protocols by numeric rank after the final '/',
// falling back to lexical order for non-numeric suffixes.
func compareProtocols(a, b Protocol) int {
	ra, oka := protocolRank(a)
	rb, okb := protocolRank(b)
	if oka && okb {
		switch {
		case ra < rb:
			return -1
		case ra > rb:
			return 1
		}
		return 0
	}
	return strings.Compare(string(a), string(b))
}

func protocolRank(p Protocol) (int, bool) {
	s := string(p)
	i := strings.LastIndexByte(s, '/')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return 0, false
	}
	return n, true
}
