package snapshotscan

import (
	"errors"
	"sort"
	"sync"
)

// ErrSnapshotExpired is returned when a cursor pins a data version that
// the store has already pruned.
var ErrSnapshotExpired = errors.New("snapshotscan: pinned snapshot version has been pruned")

// Item is a single scannable record.
type Item struct {
	Key   string
	Value string
}

// committed is one immutable data version.
type committed struct {
	version uint64
	data    map[string]string
}

// Store is a versioned key-value store. Every mutation commits a new
// version. Readers pin a version once and keep reading that exact
// snapshot no matter how the live data evolves afterwards.
type Store struct {
	mu       sync.RWMutex
	current  uint64
	versions []committed // ascending by version
}

func NewStore() *Store {
	return &Store{versions: []committed{{version: 0, data: map[string]string{}}}}
}

// Put commits an upsert and returns the new data version.
func (s *Store) Put(key, value string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := copyOf(s.versions[len(s.versions)-1].data)
	next[key] = value
	return s.commit(next)
}

// Delete commits a removal and returns the new data version.
func (s *Store) Delete(key string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := copyOf(s.versions[len(s.versions)-1].data)
	delete(next, key)
	return s.commit(next)
}

func (s *Store) commit(data map[string]string) uint64 {
	s.current++
	s.versions = append(s.versions, committed{version: s.current, data: data})
	return s.current
}

// Pin returns the current data version for a new scan.
func (s *Store) Pin() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// PruneBefore drops committed versions strictly older than keep, except
// the newest one below keep, which stays as the floor for as-of reads.
func (s *Store) PruneBefore(keep uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := 0
	for i, c := range s.versions {
		if c.version < keep {
			idx = i
		}
	}
	s.versions = append([]committed{}, s.versions[idx:]...)
}

// ScanAt returns up to limit items with Key > afterKey, read from the
// pinned snapshot version. It never observes later commits.
func (s *Store) ScanAt(version uint64, afterKey string, limit int) ([]Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, err := s.snapshotLocked(version)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(snap.data))
	for k := range snap.data {
		if k > afterKey {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	items := make([]Item, 0, len(keys))
	for _, k := range keys {
		items = append(items, Item{Key: k, Value: snap.data[k]})
	}
	return items, nil
}

// HasKeysAfter reports whether the pinned snapshot holds any key
// greater than afterKey.
func (s *Store) HasKeysAfter(version uint64, afterKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, err := s.snapshotLocked(version)
	if err != nil {
		return false, err
	}
	for k := range snap.data {
		if k > afterKey {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) snapshotLocked(version uint64) (committed, error) {
	// Newest committed version not exceeding the pinned one.
	for i := len(s.versions) - 1; i >= 0; i-- {
		if s.versions[i].version <= version {
			return s.versions[i], nil
		}
	}
	return committed{}, ErrSnapshotExpired
}

func copyOf(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
