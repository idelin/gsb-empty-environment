package netsec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// MemoryStore is a concurrency-safe in-memory Store. A zero BlockVersion marks
// a missing block, so the first write of an id must expect the zero version.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]Record)}
}

// Get returns the record for id or ErrNotFound.
func (s *MemoryStore) Get(ctx context.Context, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

// List returns all records ordered by block id for deterministic iteration.
func (s *MemoryStore) List(ctx context.Context) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BlockID < out[j].BlockID })
	return out, nil
}

// CompareAndSwap atomically replaces the record for id when its current
// version equals expected. On mismatch it returns ErrConflict and the current
// record so the caller can re-read without another lookup.
func (s *MemoryStore) CompareAndSwap(ctx context.Context, id string, expected BlockVersion, next Record) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.records[id]
	if ok && current.Version != expected {
		return current, ErrConflict
	}
	if !ok && expected != (BlockVersion{}) {
		return Record{}, ErrConflict
	}
	next.BlockID = id
	s.records[id] = next
	return Record{}, nil
}

// fileFormat is the on-disk envelope of FileStore.
type fileFormat struct {
	Format int               `json:"format"`
	Blocks map[string]Record `json:"blocks"`
}

// FileStore persists records as JSON and writes atomically (temp file plus
// rename), so a crash leaves either the old or the new state, never a torn
// write. Re-opening the same path simulates a process restart.
type FileStore struct {
	mu   sync.Mutex
	path string
	data fileFormat
}

// OpenFileStore opens (or creates) the JSON store at path.
func OpenFileStore(path string) (*FileStore, error) {
	s := &FileStore{
		path: path,
		data: fileFormat{Format: 1, Blocks: make(map[string]Record)},
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, err
		}
		if s.data.Blocks == nil {
			s.data.Blocks = make(map[string]Record)
		}
	case os.IsNotExist(err):
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s, nil
}

// Path returns the backing file path.
func (s *FileStore) Path() string { return s.path }

func (s *FileStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".netsec-store-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&s.data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

// Get returns the record for id or ErrNotFound.
func (s *FileStore) Get(ctx context.Context, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data.Blocks[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

// List returns all records ordered by block id for deterministic iteration.
func (s *FileStore) List(ctx context.Context) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.data.Blocks))
	for _, rec := range s.data.Blocks {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BlockID < out[j].BlockID })
	return out, nil
}

// CompareAndSwap applies the same version-check semantics as MemoryStore and
// durably persists every committed change before returning.
func (s *FileStore) CompareAndSwap(ctx context.Context, id string, expected BlockVersion, next Record) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Blocks[id]
	if ok && current.Version != expected {
		return current, ErrConflict
	}
	if !ok && expected != (BlockVersion{}) {
		return Record{}, ErrConflict
	}
	next.BlockID = id
	s.data.Blocks[id] = next
	if err := s.persistLocked(); err != nil {
		delete(s.data.Blocks, id)
		return Record{}, err
	}
	return Record{}, nil
}
