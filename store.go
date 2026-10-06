package mask

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"sync"
)

// ErrConflict is returned by Store.Commit when the published mapping table
// has changed since the caller loaded it. Callers should reload and retry.
var ErrConflict = errors.New("mask: mapping table changed concurrently")

// Table is the persistent mapping table. Entries maps a type-tagged
// original value to its masked JSON literal. Version is a
// compare-and-swap token: Commit only succeeds against the version the
// caller loaded, so a failed transaction never publishes partial state.
type Table struct {
	Version uint64            `json:"version"`
	Entries map[string]string `json:"entries"`
}

// Store persists the mapping table. Implementations must apply Commit
// atomically: either the whole table is published or nothing is.
type Store interface {
	Load() (Table, error)
	Commit(expectedVersion uint64, t Table) error
}

// FileStore keeps the mapping table in a single JSON file. Commits are
// written to a temporary file and atomically renamed into place, so a
// crash mid-commit leaves the previously published table intact and the
// stale temporary file is discarded on the next Load.
//
// FileStore is safe for concurrent use within a single process.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a FileStore persisting the table at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Load returns the last committed table. A leftover temporary file from
// an interrupted commit is removed and ignored.
func (s *FileStore) Load() (Table, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	os.Remove(s.path + ".tmp")
	return s.loadLocked()
}

func (s *FileStore) loadLocked() (Table, error) {
	data, err := ioutil.ReadFile(s.path)
	if os.IsNotExist(err) {
		return Table{Entries: map[string]string{}}, nil
	}
	if err != nil {
		return Table{}, err
	}
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return Table{}, err
	}
	if t.Entries == nil {
		t.Entries = map[string]string{}
	}
	return t, nil
}

// Commit atomically replaces the published table if its version still
// equals expectedVersion; otherwise it returns ErrConflict and publishes
// nothing.
func (s *FileStore) Commit(expectedVersion uint64, t Table) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.loadLocked()
	if err != nil {
		return err
	}
	if cur.Version != expectedVersion {
		return ErrConflict
	}
	t.Version = expectedVersion + 1
	data, err := json.MarshalIndent(&t, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// MemStore is an in-memory Store, useful for tests and ephemeral runs.
type MemStore struct {
	mu sync.Mutex
	t  Table
}

// NewMemStore returns an empty in-memory Store.
func NewMemStore() *MemStore {
	return &MemStore{t: Table{Entries: map[string]string{}}}
}

// Load returns a copy of the current table.
func (s *MemStore) Load() (Table, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyTable(s.t), nil
}

// Commit atomically replaces the table if the version matches.
func (s *MemStore) Commit(expectedVersion uint64, t Table) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.t.Version != expectedVersion {
		return ErrConflict
	}
	t.Version = expectedVersion + 1
	s.t = copyTable(t)
	return nil
}

func copyTable(t Table) Table {
	entries := make(map[string]string, len(t.Entries))
	for k, v := range t.Entries {
		entries[k] = v
	}
	return Table{Version: t.Version, Entries: entries}
}
