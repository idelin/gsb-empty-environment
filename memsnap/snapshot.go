// Package memsnap implements a multi-version (MVCC) in-memory key/value
// store with snapshot isolation.
//
// A single writer commits batches of key updates serially via WriteBatch.
// Readers obtain a Snapshot via BeginRead and then read without holding any
// global lock: every Get observes exactly the version pinned by the snapshot
// and never mixes pre-/post-commit data. Deletes make a key invisible to
// newer versions while older snapshots still see the old value.
//
// Linearization points:
//   - WriteBatch: the whole commit is linearized while holding Store.mu, at
//     the moment the new version number is assigned and the new version
//     nodes are published to the per-key chain heads (atomic store).
//     Validation happens before the lock is taken, so a failed batch
//     publishes nothing.
//   - BeginRead: linearized under Store.mu when the current version is
//     pinned and the snapshot is registered. Because commits and snapshot
//     creation share the same mutex, they are totally ordered.
//   - Snapshot.Get: lock-free. The snapshot's logical state is immutable
//     once pinned, so a Get linearizes anywhere between BeginRead and
//     EndRead; concretely at the atomic load of the key's chain head.
//     Reclamation guarantees the node visible to any live snapshot is
//     never unlinked, so the walk always finds it.
//   - Snapshot.EndRead: linearized at the CAS that flips closed; the
//     first successful close unregisters the snapshot and runs reclamation.
package memsnap

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrNotFound is returned by Get when the key has no visible value in the
// snapshot's version (never written, or deleted at or before that version).
var ErrNotFound = errors.New("memsnap: key not found in snapshot version")

// ErrSnapshotClosed is returned by Get on a snapshot whose EndRead was called.
var ErrSnapshotClosed = errors.New("memsnap: snapshot handle is closed")

// ErrInvalidOp is returned by WriteBatch when any op in the batch is invalid;
// the whole batch is rejected and nothing is published.
var ErrInvalidOp = errors.New("memsnap: invalid operation in batch")

// Op is a single key update inside a batch.
type Op struct {
	Key    string
	Value  string
	Delete bool // true: delete Key; false: set Key = Value
}

// Put returns an Op that sets key to value.
func Put(key, value string) Op { return Op{Key: key, Value: value} }

// Del returns an Op that deletes key.
func Del(key string) Op { return Op{Key: key, Delete: true} }

// node is one immutable versioned value on a key's version chain.
// version/value/deleted never change after publication; next is atomic so
// reclamation can unlink dead nodes while readers traverse the chain.
type node struct {
	version uint64
	value   string
	deleted bool
	next    atomic.Pointer[node]
}

// chain is the version list of one key, newest first, versions
// non-increasing from head to tail.
type chain struct {
	head atomic.Pointer[node]
}

// Store is the MVCC store. The zero value is not usable; call NewStore.
type Store struct {
	mu        sync.Mutex // serializes commits, snapshot (un)registration, reclamation
	version   uint64
	chains    map[string]*chain
	snapshots map[*Snapshot]struct{}
	retained  atomic.Int64 // live version nodes across all chains
	peak      atomic.Int64 // high-water mark of retained
}

// NewStore returns an empty store at version 0.
func NewStore() *Store {
	return &Store{
		chains:    make(map[string]*chain),
		snapshots: make(map[*Snapshot]struct{}),
	}
}

// WriteBatch validates and atomically commits a batch of key updates as one
// new version. If any op is invalid the whole batch fails and no partial
// result is published (the version counter is not even incremented).
// An empty batch is a no-op.
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

	s.version++ // linearization point of the commit begins
	v := s.version
	for _, op := range ops {
		c := s.chains[op.Key]
		if c == nil {
			c = &chain{}
			s.chains[op.Key] = c
		}
		n := &node{version: v, value: op.Value, deleted: op.Delete}
		n.next.Store(c.head.Load())
		c.head.Store(n) // publication: atomic, readers never hold mu
		s.retained.Add(1)
	}
	s.raisePeak()
	return nil
}

// BeginRead pins the current version and returns a snapshot handle.
// The caller must call EndRead exactly when done; reads after EndRead fail.
func (s *Store) BeginRead() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := &Snapshot{store: s, version: s.version}
	s.snapshots[snap] = struct{}{}
	return snap
}

// Stats reports memory-usage statistics. Retained counts the actual version
// nodes still linked in the chains (not merely the number of live keys);
// Peak is the high-water mark of Retained.
type Stats struct {
	Version   uint64
	Retained  int64
	Peak      int64
	Snapshots int
}

// Stats returns a consistent snapshot of the store's statistics.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Version:   s.version,
		Retained:  s.retained.Load(),
		Peak:      s.peak.Load(),
		Snapshots: len(s.snapshots),
	}
}

// reclaim unlinks every version node that no live snapshot can observe.
// Must be called with s.mu held.
//
// A node n is visible to a snapshot pinned at version sv iff n is the
// newest node with version <= sv. Because chain versions are non-increasing
// from head to tail, n (with immediate predecessor p) is visible iff
// n.version <= sv < p.version. The newest node is always kept for future
// readers. Nodes no snapshot needs are unlinked and become garbage.
func (s *Store) reclaim() {
	versions := make([]uint64, 0, len(s.snapshots))
	for snap := range s.snapshots {
		versions = append(versions, snap.version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	for _, c := range s.chains {
		var nodes []*node
		for n := c.head.Load(); n != nil; n = n.next.Load() {
			nodes = append(nodes, n)
		}
		if len(nodes) <= 1 {
			continue
		}
		keep := make([]*node, 0, len(nodes))
		keep = append(keep, nodes[0]) // newest: always visible to future readers
		for i := 1; i < len(nodes); i++ {
			n, pred := nodes[i], nodes[i-1]
			// visible to some live snapshot sv with n.version <= sv < pred.version
			idx := sort.Search(len(versions), func(j int) bool { return versions[j] >= n.version })
			if idx < len(versions) && versions[idx] < pred.version {
				keep = append(keep, n)
			}
		}
		if len(keep) == len(nodes) {
			continue
		}
		for i := 0; i+1 < len(keep); i++ {
			keep[i].next.Store(keep[i+1])
		}
		keep[len(keep)-1].next.Store(nil)
		c.head.Store(keep[0])
		s.retained.Add(int64(len(keep) - len(nodes)))
	}
}

func (s *Store) raisePeak() {
	for {
		r, p := s.retained.Load(), s.peak.Load()
		if r <= p || s.peak.CompareAndSwap(p, r) {
			return
		}
	}
}

// Snapshot is a read handle pinned to one store version. Not safe for
// concurrent use by multiple goroutines unless externally synchronized;
// distinct snapshots may be used concurrently.
type Snapshot struct {
	store   *Store
	version uint64
	closed  atomic.Bool
}

// Version returns the store version this snapshot is pinned to.
func (snap *Snapshot) Version() uint64 { return snap.version }

// Get returns the value of key as of the snapshot's version.
// It never blocks on the store's commit lock.
func (snap *Snapshot) Get(key string) (string, error) {
	if snap.closed.Load() {
		return "", ErrSnapshotClosed
	}
	c := snap.store.chains[key]
	if c == nil {
		return "", ErrNotFound
	}
	for n := c.head.Load(); n != nil; n = n.next.Load() {
		if n.version <= snap.version {
			if n.deleted {
				return "", ErrNotFound
			}
			return n.value, nil
		}
	}
	return "", ErrNotFound
}

// EndRead closes the handle and releases the pinned version, allowing
// reclamation of version nodes no remaining snapshot needs. It is
// idempotent: calling it more than once has no effect and returns nil.
func (snap *Snapshot) EndRead() error {
	if !snap.closed.CompareAndSwap(false, true) {
		return nil // already closed: idempotent
	}
	s := snap.store
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, snap)
	s.reclaim()
	return nil
}
