package kvimport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoPublished is returned by LoadPublished when no finished store has
// been published in the directory yet.
var ErrNoPublished = errors.New("kvimport: no published store")

// VersionedValue is a value together with its version. The version is the
// byte offset of the source record in the input file, which makes
// application idempotent: replaying the same record (same offset) never
// bumps the version.
type VersionedValue struct {
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

// Store is an in-memory versioned key-value store rebuilt from the WAL
// during recovery and snapshot-published at the end of an import.
type Store struct {
	entries map[string]VersionedValue
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{entries: make(map[string]VersionedValue)}
}

// Apply sets key to value sourced from the given input offset. It returns
// true if the value changed. A record whose offset is not newer than the
// stored version is ignored, so replaying an already-applied record is a
// no-op and versions never grow on replay.
func (s *Store) Apply(offset int64, key, value string) bool {
	if cur, ok := s.entries[key]; ok && cur.Version >= offset {
		return false
	}
	s.entries[key] = VersionedValue{Value: value, Version: offset}
	return true
}

// Get returns the value for key.
func (s *Store) Get(key string) (VersionedValue, bool) {
	v, ok := s.entries[key]
	return v, ok
}

// Len returns the number of keys.
func (s *Store) Len() int {
	return len(s.entries)
}

// Keys returns the keys in unspecified order.
func (s *Store) Keys() []string {
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	return keys
}

// snapshot serializes the store deterministically (encoding/json sorts
// map keys), so identical logical content produces identical bytes.
func (s *Store) snapshot() []byte {
	data, err := json.Marshal(s.entries)
	if err != nil {
		panic(err) // map[string]VersionedValue cannot fail to marshal
	}
	return data
}

// LoadPublished reads the atomically published store from dir. It never
// observes a partially written batch: the file only appears via an atomic
// rename after the import has fully finished.
func LoadPublished(dir string) (*Store, error) {
	data, err := os.ReadFile(filepath.Join(dir, publishedName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoPublished
	}
	if err != nil {
		return nil, err
	}
	var entries map[string]VersionedValue
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("corrupt published store: %w", err)
	}
	return &Store{entries: entries}, nil
}

// walEntry is one confirmed record in data.wal.
type walEntry struct {
	Offset int64  `json:"offset"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

// loadWal rebuilds a store by replaying the WAL. The WAL must already be
// truncated to the confirmed length before calling.
func loadWal(path string) (*Store, error) {
	store := NewStore()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var e walEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("corrupt wal entry: %w", err)
		}
		store.Apply(e.Offset, e.Key, e.Value)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return store, nil
}
