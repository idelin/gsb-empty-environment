// Package versionedkv implements an in-memory key-value store with
// version-conditional writes (compare-and-swap) and atomic multi-condition
// batches.
//
// Every key carries a Version drawn from a single monotonically increasing
// revision counter. Because revisions are never reused, a version that has
// been invalidated can never become current again: deleting and recreating
// a key always yields a fresh version, so stale conditions stay false (no
// ABA problem).
//
// Missing keys have their own per-key "missing" version (Exists == false),
// anchored at the key's tombstone revision. A missing version is therefore
// independent of any present version and of other keys, and delete/recreate
// cycles move it forward as well.
package versionedkv

import "sync"

// Version identifies one exact state of one key. It is only meaningful for
// the key it was obtained from; comparing versions across keys is invalid
// and will never match inside a Condition.
type Version struct {
	Key    string
	Rev    uint64
	Exists bool
}

// Condition requires a key to be at an exact version for a batch to apply.
type Condition struct {
	expected Version
}

// IfVersion builds a condition that holds only while the key referenced by
// v is still at version v. A condition on a missing version acts as a
// "create only if still absent" guard.
func IfVersion(v Version) Condition {
	return Condition{expected: v}
}

// Write is a single mutation inside a batch.
type Write struct {
	key    string
	value  []byte
	delete bool
}

// Put returns a write that sets key to value.
func Put(key string, value []byte) Write {
	return Write{key: key, value: value}
}

// Delete returns a write that removes key.
func Delete(key string) Write {
	return Write{key: key, delete: true}
}

type entry struct {
	value []byte
	rev   uint64
}

// Store is a versioned in-memory key-value store. It is safe for concurrent
// use.
type Store struct {
	mu sync.Mutex
	// rev is the global revision counter. It only ever increases, which is
	// what makes ABA impossible: every mutation hands out a brand new
	// revision, so no past version can ever become current again.
	rev uint64
	// items holds live keys with the revision of their last write.
	items map[string]entry
	// tombs holds the deletion revision of absent keys, giving every
	// missing key its own independent, still monotonic, missing version.
	tombs map[string]uint64
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		items: make(map[string]entry),
		tombs: make(map[string]uint64),
	}
}

// VersionOf returns the current version of key, present or missing.
func (s *Store) VersionOf(key string) Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versionOfLocked(key)
}

func (s *Store) versionOfLocked(key string) Version {
	if e, ok := s.items[key]; ok {
		return Version{Key: key, Rev: e.rev, Exists: true}
	}
	return Version{Key: key, Rev: s.tombs[key], Exists: false}
}

// Get returns the value and current version of key. The value is nil when
// the key is missing; the version is always meaningful and can be used in
// IfVersion conditions.
func (s *Store) Get(key string) ([]byte, Version) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.items[key]; ok {
		return append([]byte(nil), e.value...), s.versionOfLocked(key)
	}
	return nil, s.versionOfLocked(key)
}

// CompareAndSwap applies a single write if cond holds.
func (s *Store) CompareAndSwap(cond Condition, w Write) bool {
	return s.Batch([]Condition{cond}, []Write{w})
}

// Batch applies writes atomically if and only if every condition holds at
// the same instant. If any condition fails, nothing is written and false is
// returned. An empty batch (no conditions, no writes) succeeds trivially
// and changes nothing.
func (s *Store) Batch(conds []Condition, writes []Write) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range conds {
		if s.versionOfLocked(c.expected.Key) != c.expected {
			return false
		}
	}
	for _, w := range writes {
		s.applyLocked(w)
	}
	return true
}

func (s *Store) applyLocked(w Write) {
	s.rev++
	if w.delete {
		delete(s.items, w.key)
		s.tombs[w.key] = s.rev
		return
	}
	s.items[w.key] = entry{value: append([]byte(nil), w.value...), rev: s.rev}
}
