// Package mvcc implements a minimal MVCC (multi-version concurrency
// control) store. Every write prepends a new version to the head of a
// per-key chain ordered by descending commit timestamp. Readers walk
// the chain to find the newest version visible to their read view.
// The store tracks the global minimum active read view, and a garbage
// collector prunes versions that are overwritten and invisible to
// every active and future reader.
package mvcc

import "sync"

// Version is one committed version of a data item. Versions of the
// same item are linked newest-first (descending commit timestamp).
type Version struct {
	TS    uint64   // commit timestamp
	Value string   // payload
	Next  *Version // next (older) version
}

type chain struct {
	head *Version
}

// Store is an MVCC key-value store safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	clock   uint64 // highest commit timestamp issued so far
	chains  map[string]*chain
	readers map[uint64]int // active read-view timestamps -> refcount
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{
		chains:  make(map[string]*chain),
		readers: make(map[uint64]int),
	}
}

// Write commits value as a new version of key, assigning the next
// commit timestamp from the store clock.
func (s *Store) Write(key, value string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(key, value, s.clock+1)
}

// WriteAt commits value as a new version of key at an explicit commit
// timestamp. The timestamp must not go backwards for the same key.
func (s *Store) WriteAt(key, value string, ts uint64) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(key, value, ts)
}

func (s *Store) writeLocked(key, value string, ts uint64) uint64 {
	c := s.chains[key]
	if c == nil {
		c = &chain{}
		s.chains[key] = c
	}
	if c.head != nil && ts < c.head.TS {
		panic("mvcc: commit timestamp goes backwards")
	}
	c.head = &Version{TS: ts, Value: value, Next: c.head}
	if ts > s.clock {
		s.clock = ts
	}
	return ts
}

// GetAt returns the newest version of key visible at read timestamp
// ts: the first version on the chain whose commit timestamp is <= ts.
func (s *Store) GetAt(key string, ts uint64) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.chains[key]
	if c == nil {
		return "", false
	}
	for v := c.head; v != nil; v = v.Next {
		if v.TS <= ts {
			return v.Value, true
		}
	}
	return "", false
}

// ReadView is a registered snapshot read timestamp. While a ReadView
// is open it pins the GC horizon: versions still visible to it are
// never pruned.
type ReadView struct {
	store  *Store
	ts     uint64
	closed bool
}

// BeginRead opens a read view at the store's current commit timestamp.
func (s *Store) BeginRead() *ReadView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beginReadLocked(s.clock)
}

// BeginReadAt opens a read view pinned at an explicit timestamp,
// modelling a long-running read transaction that started earlier.
func (s *Store) BeginReadAt(ts uint64) *ReadView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beginReadLocked(ts)
}

func (s *Store) beginReadLocked(ts uint64) *ReadView {
	s.readers[ts]++
	return &ReadView{store: s, ts: ts}
}

// TS returns the read view's snapshot timestamp.
func (rv *ReadView) TS() uint64 { return rv.ts }

// Get returns the newest version of key visible to this read view.
func (rv *ReadView) Get(key string) (string, bool) {
	return rv.store.GetAt(key, rv.ts)
}

// Close releases the read view so it no longer pins the GC horizon.
func (rv *ReadView) Close() {
	s := rv.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if rv.closed {
		return
	}
	rv.closed = true
	if n := s.readers[rv.ts]; n <= 1 {
		delete(s.readers, rv.ts)
	} else {
		s.readers[rv.ts] = n - 1
	}
}

// MinActiveRead returns the smallest timestamp among all active read
// views, or false if there are none.
func (s *Store) MinActiveRead() (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.minActiveReadLocked()
}

func (s *Store) minActiveReadLocked() (uint64, bool) {
	var min uint64
	found := false
	for ts := range s.readers {
		if !found || ts < min {
			min, found = ts, true
		}
	}
	return min, found
}

// GC prunes obsolete versions from every chain and returns the number
// of pruned versions.
//
// The GC horizon is the global minimum active read-view timestamp (or
// the latest commit timestamp when no reader is active). On each chain
// the newest version visible at the horizon is kept; every older
// version is covered by it and can never be observed by any active or
// future reader, so it is unlinked and becomes reclaimable.
func (s *Store) GC() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	horizon, ok := s.minActiveReadLocked()
	if !ok {
		horizon = s.clock
	}
	pruned := 0
	for _, c := range s.chains {
		v := c.head
		for v != nil && v.TS > horizon {
			v = v.Next
		}
		if v == nil || v.Next == nil {
			continue
		}
		for p := v.Next; p != nil; p = p.Next {
			pruned++
		}
		v.Next = nil
	}
	return pruned
}

// Versions returns the commit timestamps currently on key's chain,
// newest first. Intended for tests and diagnostics.
func (s *Store) Versions(key string) []uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []uint64
	if c := s.chains[key]; c != nil {
		for v := c.head; v != nil; v = v.Next {
			out = append(out, v.TS)
		}
	}
	return out
}
