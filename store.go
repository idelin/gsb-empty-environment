package recovery

import (
	"encoding/json"
	"os"
	"sync"
)

// Record is the persisted outcome of one task execution.
type Record struct {
	OK           bool   // whether the task completed successfully
	InputDigest  string // digest of the inputs the record was built from
	Output       []byte // task output, handed to dependents on reuse
	OutputDigest string // digest of Output
}

// Store persists task records across planner runs.
type Store interface {
	Get(task string) (Record, bool)
	Put(task string, rec Record) error
}

// MemStore is an in-memory Store.
type MemStore struct {
	mu   sync.Mutex
	recs map[string]Record
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{recs: map[string]Record{}}
}

// Get implements Store.
func (s *MemStore) Get(task string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[task]
	return rec, ok
}

// Put implements Store.
func (s *MemStore) Put(task string, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[task] = rec
	return nil
}

// FileStore is a JSON-file-backed Store so recovery state survives
// process restarts.
type FileStore struct {
	mu   sync.Mutex
	path string
	recs map[string]Record
}

// NewFileStore loads (or lazily creates) a store at path.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, recs: map[string]Record{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.recs); err != nil {
		return nil, err
	}
	return s, nil
}

// Get implements Store.
func (s *FileStore) Get(task string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[task]
	return rec, ok
}

// Put implements Store, persisting the whole store atomically.
func (s *FileStore) Put(task string, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[task] = rec
	b, err := json.Marshal(s.recs)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
