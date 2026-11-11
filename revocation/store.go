package revocation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// MemoryStore keeps state in process memory. It satisfies [StateStore] and is
// the default when no store is configured.
type MemoryStore struct {
	mu    sync.Mutex
	state PersistedState
	ok    bool
}

// Save records a defensive copy of the state.
func (m *MemoryStore) Save(state PersistedState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
	m.ok = true
	return nil
}

// Load returns the last saved state, if any.
func (m *MemoryStore) Load() (PersistedState, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, m.ok, nil
}

// FileStore persists state as JSON in a single file. Saves are atomic and
// durable: the payload is written to a temporary file in the same directory,
// fsynced, renamed over the target, and the directory is fsynced.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a store rooted at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Save atomically replaces the state file.
func (f *FileStore) Save(state PersistedState) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("revocation: encode state: %w", err)
	}

	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("revocation: create state dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".revocation-state-*.tmp")
	if err != nil {
		return fmt.Errorf("revocation: create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("revocation: write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("revocation: sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("revocation: close temp state: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		return fmt.Errorf("revocation: replace state file: %w", err)
	}

	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("revocation: open state dir: %w", err)
	}
	defer func() { _ = dirHandle.Close() }()
	if err := dirHandle.Sync(); err != nil {
		return fmt.Errorf("revocation: sync state dir: %w", err)
	}
	return nil
}

// Load reads the state file. A missing file reports ok=false; a corrupt file
// is an error so callers can decide to fail closed instead of silently
// restarting from an empty view.
func (f *FileStore) Load() (PersistedState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	payload, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return PersistedState{}, false, nil
	}
	if err != nil {
		return PersistedState{}, false, fmt.Errorf("revocation: read state: %w", err)
	}

	var state PersistedState
	if err := json.Unmarshal(payload, &state); err != nil {
		return PersistedState{}, false, fmt.Errorf("revocation: decode state: %w", err)
	}
	return state, true, nil
}
