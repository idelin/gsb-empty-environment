package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrNoState means that no previously persisted state exists. Open treats it
// as a new, uninitialised trust-anchor set.
var ErrNoState = errors.New("netsec: no persisted state")

// Store persists the complete state blob. Save must replace the previous blob
// atomically: a crash must leave either the old state or the new state, never a
// partially written mixture.
type Store interface {
	Load() ([]byte, error)
	Save([]byte) error
}

// MemoryStore is primarily useful in tests. It retains data across Machine
// instances, but not across process restarts.
type MemoryStore struct {
	mu   sync.RWMutex
	data []byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) Load() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return nil, ErrNoState
	}
	return append([]byte(nil), s.data...), nil
}

func (s *MemoryStore) Save(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append([]byte(nil), data...)
	return nil
}

// FileStore stores the state in a private file and replaces it atomically with
// rename after fsyncing the new contents.
type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Path() string {
	return s.path
}

func (s *FileStore) Load() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, fmt.Errorf("netsec: load state: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty state file", ErrCorruptState)
	}
	return data, nil
}

func (s *FileStore) Save(data []byte) error {
	if s.path == "" {
		return errors.New("netsec: empty state file path")
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".netsec-anchors-*")
	if err != nil {
		return fmt.Errorf("netsec: create state file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("netsec: set state file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("netsec: write state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("netsec: sync state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("netsec: close state file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("netsec: replace state file: %w", err)
	}

	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("netsec: open state directory: %w", err)
	}
	defer dirFile.Close()
	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("netsec: sync state directory: %w", err)
	}
	return nil
}
