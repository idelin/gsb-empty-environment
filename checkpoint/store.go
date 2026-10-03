// Package checkpoint implements a log-structured incremental checkpoint
// generator for an in-memory state machine.
//
// The state tree is sliced into fixed pages. Checkpoints pin the current
// pages as a read-only snapshot (writes use copy-on-write), serialize dirty
// pages into chunks in parallel, and chain per-chunk SHA-256 hashes so that
// recovery can verify integrity and pinpoint corrupted pages.
package checkpoint

import (
	"hash/fnv"
	"sync"
)

// NumPages is the number of fixed pages the state tree is sliced into.
const NumPages = 256

// page is an immutable-once-pinned slice of the state tree.
type page struct {
	entries map[string]string
	version uint64 // store version at last modification
	pinned  bool   // referenced by an in-flight snapshot
}

func (p *page) clone() *page {
	np := &page{entries: make(map[string]string, len(p.entries)), version: p.version}
	for k, v := range p.entries {
		np.entries[k] = v
	}
	return np
}

// Store is the in-memory state machine: a versioned KV store sharded by page.
type Store struct {
	mu      sync.RWMutex
	pages   [NumPages]*page
	version uint64
}

// NewStore returns an empty store with all pages initialized.
func NewStore() *Store {
	s := &Store{}
	for i := range s.pages {
		s.pages[i] = &page{entries: make(map[string]string)}
	}
	return s
}

func pageIndex(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % NumPages)
}

// Put inserts or updates a key. If the target page is pinned by a snapshot,
// the page is copied first (copy-on-write) so the snapshot stays consistent.
func (s *Store) Put(key, value string) {
	s.mu.Lock()
	s.version++
	idx := pageIndex(key)
	p := s.pages[idx]
	if p.pinned {
		p = p.clone()
		s.pages[idx] = p
	}
	p.entries[key] = value
	p.version = s.version
	s.mu.Unlock()
}

// Get returns the value for key.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	v, ok := s.pages[pageIndex(key)].entries[key]
	s.mu.RUnlock()
	return v, ok
}

// Version returns the current monotonic store version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Snapshot is a read-only, consistent view of the store at a version.
type Snapshot struct {
	store   *Store
	pages   [NumPages]*page
	version uint64
}

// BeginSnapshot pins all current pages. Subsequent writes copy-on-write,
// so the snapshot observes a consistent state while writes continue.
func (s *Store) BeginSnapshot() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := &Snapshot{store: s, version: s.version}
	for i, p := range s.pages {
		p.pinned = true
		snap.pages[i] = p
	}
	return snap
}

// Release unpins the snapshot pages.
func (snap *Snapshot) Release() {
	snap.store.mu.Lock()
	defer snap.store.mu.Unlock()
	for _, p := range snap.pages {
		p.pinned = false
	}
}
