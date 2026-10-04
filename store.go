// Package snapshotstore implements a multi-version snapshot key/value store
// for memory-resident configuration.
//
// Concurrency model and linearization points:
//
//   - WriteBatch: a single writer mutex serializes commits. A batch is first
//     validated in full; if any op is invalid, nothing is mutated and no new
//     version is published (no half-applied batches). The linearization point
//     of a successful commit is the atomic swap of the root table pointer
//     (root.Store), which publishes the whole batch as one new version.
//   - BeginRead: linearizes under the same mutex: it reads the current
//     version and registers itself as a live reader atomically with respect
//     to commits and reclamation.
//   - Snapshot.Get: lock-free. It atomically loads the root table pointer and
//     walks immutable per-key version chains, selecting the newest entry with
//     version <= the snapshot version. A reader therefore observes exactly one
//     version and never mixes pre/post-commit data, without holding any
//     global read lock.
//   - Snapshot.EndRead: linearizes at the atomic CAS that flips the closed
//     flag; only the first successful CAS unregisters the reader, so repeated
//     closes are idempotent.
//   - Reclaim: computes the minimum version still needed (the latest version
//     and all live reader versions) and drops only chain nodes no live reader
//     can observe. Nodes are never mutated in place; reclamation builds new
//     chains and swaps the root, so in-flight readers are unaffected.
package snapshotstore

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrSnapshotClosed is returned by Get on a closed snapshot handle.
var ErrSnapshotClosed = errors.New("snapshotstore: snapshot is closed")

// ErrInvalidOp is returned when a batch contains an invalid operation.
// The whole batch is rejected and nothing is published.
var ErrInvalidOp = errors.New("snapshotstore: invalid operation (empty key)")

// Op is a single key update within a batch. Delete marks the key as removed
// in the new version; older versions keep the old value for existing readers.
type Op struct {
	Key    string
	Value  string
	Delete bool
}

// node is one immutable entry in a per-key version chain.
// Chains are ordered newest-first (strictly descending versions).
type node struct {
	version uint64
	value   string
	deleted bool
	next    *node
}

// table is an immutable snapshot of all per-key chains.
type table struct {
	m map[string]*node
}

// Stats reports memory and version usage. RetainedNodes counts the actual
// version nodes kept alive across all retained versions (not just the number
// of latest keys); PeakNodes is the high-water mark of RetainedNodes.
type Stats struct {
	Version       uint64
	LatestKeys    int
	LiveReaders   int
	RetainedNodes int
	PeakNodes     int
}

// Store is a multi-version snapshot store. The zero value is not usable;
// construct it with New.
type Store struct {
	mu       sync.Mutex // serializes writer, reader registration, reclaimer
	root     atomic.Pointer[table]
	version  atomic.Uint64
	readers  map[*Snapshot]uint64
	retained int
	peak     int
}

// New returns an empty store at version 0.
func New() *Store {
	s := &Store{readers: make(map[*Snapshot]uint64)}
	s.root.Store(&table{m: make(map[string]*node)})
	return s
}

// WriteBatch atomically commits ops as one new version. If any op is invalid
// the whole batch is rejected and no partial result is published. Writes are
// serialized: concurrent callers commit one batch at a time.
func (s *Store) WriteBatch(ops []Op) error {
	for _, op := range ops {
		if op.Key == "" {
			return ErrInvalidOp
		}
	}
	if len(ops) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.root.Load()
	next := make(map[string]*node, len(old.m))
	for k, head := range old.m {
		next[k] = head
	}
	v := s.version.Load() + 1
	for _, op := range ops {
		next[op.Key] = &node{
			version: v,
			value:   op.Value,
			deleted: op.Delete,
			next:    next[op.Key],
		}
	}
	// Linearization point: the whole batch becomes visible atomically.
	s.root.Store(&table{m: next})
	s.version.Store(v)

	s.retained += len(ops)
	if s.retained > s.peak {
		s.peak = s.retained
	}
	return nil
}

// BeginRead returns a snapshot handle pinned to the current version. The
// caller must call EndRead when done so pinned versions can be reclaimed.
func (s *Store) BeginRead() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.version.Load()
	snap := &Snapshot{store: s, version: v}
	s.readers[snap] = v
	return snap
}

// Reclaim drops version nodes that no live reader (or the latest version)
// can observe, and returns the number of nodes freed. Nodes still needed by
// any open snapshot are always retained.
func (s *Store) Reclaim() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	minNeeded := s.version.Load()
	for _, v := range s.readers {
		if v < minNeeded {
			minNeeded = v
		}
	}

	old := s.root.Load()
	next := make(map[string]*node, len(old.m))
	retained := 0
	for key, head := range old.m {
		// Keep every node newer than minNeeded plus the single floor node
		// visible at minNeeded; everything older is unreachable.
		var kept []*node
		for n := head; n != nil; n = n.next {
			kept = append(kept, n)
			if n.version <= minNeeded {
				break
			}
		}
		// The key is invisible to every live reader only if all surviving
		// entries are tombstones; then the whole chain can be dropped.
		allDeleted := true
		for _, n := range kept {
			if !n.deleted {
				allDeleted = false
				break
			}
		}
		if allDeleted {
			continue
		}
		var newHead *node
		for i := len(kept) - 1; i >= 0; i-- {
			newHead = &node{version: kept[i].version, value: kept[i].value, deleted: kept[i].deleted, next: newHead}
		}
		next[key] = newHead
		retained += len(kept)
	}
	freed := s.retained - retained
	s.root.Store(&table{m: next})
	s.retained = retained
	return freed
}

// Stats returns current memory/version statistics. RetainedNodes reflects
// the real number of version nodes kept, across all retained versions.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Version:       s.version.Load(),
		LatestKeys:    len(s.root.Load().m),
		LiveReaders:   len(s.readers),
		RetainedNodes: s.retained,
		PeakNodes:     s.peak,
	}
}

// Snapshot is a read handle pinned to one store version. All reads through it
// observe exactly that version. It is safe for concurrent use.
type Snapshot struct {
	store   *Store
	version uint64
	closed  atomic.Bool
}

// Version returns the store version this snapshot is pinned to.
func (s *Snapshot) Version() uint64 { return s.version }

// Get returns the value of key as of the snapshot version. The boolean
// reports whether the key existed (and was not deleted) at that version.
// Reading a closed snapshot returns ErrSnapshotClosed.
func (s *Snapshot) Get(key string) (string, bool, error) {
	if s.closed.Load() {
		return "", false, ErrSnapshotClosed
	}
	t := s.store.root.Load()
	for n := t.m[key]; n != nil; n = n.next {
		if n.version <= s.version {
			if n.deleted {
				return "", false, nil
			}
			return n.value, true, nil
		}
	}
	return "", false, nil
}

// EndRead closes the handle and releases its pinned version for reclamation.
// It is idempotent: calling it more than once has no further effect.
func (s *Snapshot) EndRead() {
	if s.closed.CompareAndSwap(false, true) {
		s.store.mu.Lock()
		delete(s.store.readers, s)
		s.store.mu.Unlock()
	}
}
