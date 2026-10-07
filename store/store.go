// Package store implements a versioned key-value repository supporting
// compare-and-swap updates and atomic multi-condition batch transactions.
//
// Semantics:
//   - Every key carries a monotonically increasing version that starts at 0
//     and is bumped by exactly 1 on every mutation (create, update, delete).
//   - A missing key still has a version (its "missing version"): 0 if it was
//     never written, otherwise the version left behind by its last delete.
//     Missing versions evolve independently per key.
//   - A Condition{Key, Version} holds iff the key's current version equals
//     Version, regardless of whether the key is present. Conditions can
//     therefore assert both presence and absence of a key.
//   - Because deletes bump the version and tombstones are retained, a
//     delete followed by a recreate always yields a fresh, higher version:
//     stale conditions can never become true again (no ABA).
//   - Tombstone records are kept forever; this is the price of ABA safety.
package store

import "sync"

// Version is a per-key monotonically increasing mutation counter.
type Version uint64

// Condition asserts that Key currently has the given version.
type Condition struct {
	Key     string
	Version Version
}

// Write is a single mutation inside a batch. Del deletes the key,
// otherwise the key is set to Value.
type Write struct {
	Key   string
	Value int
	Del   bool
}

type record struct {
	value   int
	version Version
	exists  bool
}

// Store is a versioned key-value repository. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	records map[string]*record
}

// New returns an empty Store.
func New() *Store {
	return &Store{records: make(map[string]*record)}
}

// Get returns the value and current version of key. For a missing key it
// returns exists=false together with the key's independent missing version.
func (s *Store) Get(key string) (value int, version Version, exists bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[key]
	if r == nil {
		return 0, 0, false
	}
	return r.value, r.version, r.exists
}

// VersionOf returns the current version of key, present or missing.
func (s *Store) VersionOf(key string) Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versionOf(key)
}

func (s *Store) versionOf(key string) Version {
	if r := s.records[key]; r != nil {
		return r.version
	}
	return 0
}

// Txn atomically evaluates every condition against a single snapshot and,
// only if all of them hold, applies every write in order. It reports
// whether the writes were applied. A failed Txn changes nothing.
// An empty condition set always holds; an empty write set is a no-op.
func (s *Store) Txn(conds []Condition, writes []Write) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range conds {
		if s.versionOf(c.Key) != c.Version {
			return false
		}
	}
	for _, w := range writes {
		r := s.records[w.Key]
		if r == nil {
			r = &record{}
			s.records[w.Key] = r
		}
		r.version++
		if w.Del {
			r.exists = false
			r.value = 0
		} else {
			r.exists = true
			r.value = w.Value
		}
	}
	return true
}

// CompareAndSwap sets key to value iff its current version equals expect.
func (s *Store) CompareAndSwap(key string, expect Version, value int) bool {
	return s.Txn(
		[]Condition{{Key: key, Version: expect}},
		[]Write{{Key: key, Value: value}},
	)
}

// CompareAndDelete deletes key iff its current version equals expect.
func (s *Store) CompareAndDelete(key string, expect Version) bool {
	return s.Txn(
		[]Condition{{Key: key, Version: expect}},
		[]Write{{Key: key, Del: true}},
	)
}
