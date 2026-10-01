// Package lockfreemvcc implements a lock-free MVCC read path for
// read-heavy workloads.
//
// Design:
//   - Each key owns an immutable singly-linked version chain. The head
//     pointer is swapped atomically; version nodes are never mutated in
//     place (the only exception is GC tail-truncation, which is monotonic
//     and safe for in-flight readers, see gcHorizon).
//   - A global atomic commit counter defines snapshot timestamps. A
//     read-only transaction is just a load of that counter: it takes no
//     lock of any kind and traverses version chains freely, picking the
//     newest version with commitTS <= snapshotTS.
//   - Writers serialize on a mutex (writes may wait on each other, reads
//     never wait on anything). A multi-key commit links all new version
//     nodes first and publishes the commit timestamp last, so a reader
//     either sees every version of a commit or none of them: no torn
//     reads, no half-dirty data.
//   - Old versions are reclaimed by the writer via a GC horizon protocol
//     that never blocks readers: readers publish their snapshot timestamp
//     in a lock-free slot registry, and GC only truncates versions that
//     no registered (or concurrently registering) reader can observe.
package lockfreemvcc

import (
	"sync"
	"sync/atomic"
)

// numSlots is the size of the active-reader registry. It bounds the
// number of concurrently open snapshots.
const numSlots = 1024

// gcInterval controls how often (in commits) the writer attempts to
// reclaim unreachable versions.
const gcInterval = 1024

// version is an immutable MVCC version node.
type version struct {
	commitTS uint64
	value    int64
	next     atomic.Pointer[version]
}

// keyEntry is the lock-free head of one key's version chain.
type keyEntry struct {
	head atomic.Pointer[version]
}

// Mutation is a single key assignment inside one atomic commit.
type Mutation struct {
	Key   int
	Value int64
}

// Store is a multi-version key-value store with a lock-free read path.
type Store struct {
	keys      []keyEntry
	commitTS  atomic.Uint64
	gcHorizon atomic.Uint64
	slots     [numSlots]atomic.Uint64
	rotor     atomic.Uint64
	writeMu   sync.Mutex // serializes writers only; readers never touch it
}

// NewStore creates a store with numKeys keys. Seed must be called for
// every key before serving traffic.
func NewStore(numKeys int) *Store {
	s := &Store{keys: make([]keyEntry, numKeys)}
	// Commit timestamps start at 1 so that slot value 0 always means
	// "empty slot" in the reader registry.
	s.commitTS.Store(1)
	return s
}

// NumKeys returns the number of keys in the store.
func (s *Store) NumKeys() int { return len(s.keys) }

// Seed installs the initial version of a key. It must be called before
// the store serves concurrent traffic.
func (s *Store) Seed(key int, value int64) {
	s.keys[key].head.Store(&version{commitTS: 1, value: value})
}

// Commit atomically applies a batch of mutations as one version.
// Writers serialize on an internal mutex; readers are never involved.
func (s *Store) Commit(muts ...Mutation) uint64 {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.commitLocked(muts...)
}

// Transfer atomically moves delta units from one key to another,
// preserving the global sum. It is the canonical read-modify-write
// transaction for conservation tests.
func (s *Store) Transfer(from, to int, delta int64) uint64 {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	fromVal := s.keys[from].head.Load().value
	toVal := s.keys[to].head.Load().value
	return s.commitLocked(
		Mutation{Key: from, Value: fromVal - delta},
		Mutation{Key: to, Value: toVal + delta},
	)
}

// commitLocked links all new version nodes first, then publishes the
// commit timestamp. A reader whose snapshot covers ts is therefore
// guaranteed to already find every node of this commit in the chains.
func (s *Store) commitLocked(muts ...Mutation) uint64 {
	ts := s.commitTS.Load() + 1
	for _, m := range muts {
		e := &s.keys[m.Key]
		v := &version{commitTS: ts, value: m.Value}
		v.next.Store(e.head.Load())
		e.head.Store(v)
	}
	s.commitTS.Store(ts)
	if ts%gcInterval == 0 {
		s.gc()
	}
	return ts
}

// Snapshot is a read-only transaction. It is created without any lock
// and observes a consistent, torn-free view of the store at ts.
// A Snapshot must be Closed to release its reader-registry slot.
type Snapshot struct {
	store *Store
	ts    uint64
	slot  int
}

// Begin opens a read-only snapshot transaction. It never blocks and
// never acquires a lock.
func (s *Store) Begin() *Snapshot {
	for {
		ts := s.commitTS.Load()
		idx := int(s.rotor.Add(1) % numSlots)
		if s.slots[idx].CompareAndSwap(0, ts) {
			// If GC published a horizon newer than our snapshot
			// before it could observe our registration, the
			// versions we need may be gone: retry with a fresh ts.
			if s.gcHorizon.Load() <= ts {
				return &Snapshot{store: s, ts: ts, slot: idx}
			}
			s.slots[idx].Store(0)
		}
	}
}

// Get returns the value of key visible to this snapshot by walking the
// immutable version chain without any synchronization.
func (sn *Snapshot) Get(key int) int64 {
	for v := sn.store.keys[key].head.Load(); v != nil; v = v.next.Load() {
		if v.commitTS <= sn.ts {
			return v.value
		}
	}
	return 0
}

// TS returns the snapshot's commit timestamp.
func (sn *Snapshot) TS() uint64 { return sn.ts }

// Close releases the snapshot's reader-registry slot.
func (sn *Snapshot) Close() {
	sn.store.slots[sn.slot].Store(0)
}

// gc reclaims versions no active or future reader can observe.
//
// Protocol (two-phase, never blocks readers):
//  1. candidate = min(active reader snapshots, current commitTS).
//  2. Publish gcHorizon = candidate, then re-scan the registry. A reader
//     that registered with ts <= candidate after our first scan is
//     guaranteed to see the new horizon and retry (see Begin), so if any
//     slot still holds ts < candidate we simply abort this pass.
//  3. Truncate each chain just below the newest version visible at
//     candidate. Readers with ts >= candidate keep finding their
//     version; readers with ts < candidate cannot exist.
func (s *Store) gc() {
	candidate := s.commitTS.Load()
	for i := range s.slots {
		if ts := s.slots[i].Load(); ts != 0 && ts < candidate {
			candidate = ts
		}
	}
	if candidate <= s.gcHorizon.Load() {
		return
	}
	s.gcHorizon.Store(candidate)
	for i := range s.slots {
		if ts := s.slots[i].Load(); ts != 0 && ts < candidate {
			return // a reader registered concurrently; abort this pass
		}
	}
	for i := range s.keys {
		for v := s.keys[i].head.Load(); v != nil; v = v.next.Load() {
			if v.commitTS <= candidate {
				v.next.Store(nil)
				break
			}
		}
	}
}

// chainLen reports the length of a key's version chain. Used by tests.
func (s *Store) chainLen(key int) int {
	n := 0
	for v := s.keys[key].head.Load(); v != nil; v = v.next.Load() {
		n++
	}
	return n
}
