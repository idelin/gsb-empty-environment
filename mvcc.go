// Package mvcc implements a multi-version key-value store with snapshot
// isolation, atomic batch commits, and GC bounded by the oldest active
// snapshot. Clock, data source, and failure injection are all provided
// as injectable functions; there is no remote cache dependency.
package mvcc

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrSnapshotClosed is returned when reading from a closed snapshot.
	ErrSnapshotClosed = errors.New("mvcc: snapshot is closed")
	// ErrSnapshotExpired is returned when reading from a snapshot whose
	// TTL has elapsed. Expiry is terminal: the snapshot is released
	// automatically and no longer pins garbage collection.
	ErrSnapshotExpired = errors.New("mvcc: snapshot expired")
)

// Clock returns the current time. Injectable for deterministic tests.
type Clock func() time.Time

// Loader fetches a key from the underlying data source when the store
// holds no in-memory version for it. Injectable; the store itself keeps
// no remote cache.
type Loader func(key string) (value string, ok bool, err error)

// FailHook runs before internal operations ("begin", "commit", "get").
// Returning a non-nil error aborts the operation before any state
// change, which keeps batch commits atomic under injected failures.
type FailHook func(op string) error

// Options configures a Store. Nil fields get safe defaults.
type Options struct {
	Now  Clock
	Load Loader
	Fail FailHook
	// SnapshotTTL bounds snapshot lifetime. Zero means snapshots never
	// expire. Expired snapshots fail reads with ErrSnapshotExpired and
	// are released automatically.
	SnapshotTTL time.Duration
}

// versionedValue is one committed version of a key.
type versionedValue struct {
	version uint64
	value   string
	deleted bool
}

// Op is a single write inside an atomic batch.
type Op struct {
	Key    string
	Value  string
	Delete bool
}

// Put returns an Op that sets key to value.
func Put(key, value string) Op { return Op{Key: key, Value: value} }

// Del returns an Op that deletes key. The tombstone keeps the key
// visible to older snapshots while hiding it from newer ones.
func Del(key string) Op { return Op{Key: key, Delete: true} }

// Store is a multi-version key-value store. Safe for concurrent use.
type Store struct {
	mu        sync.RWMutex
	data      map[string][]versionedValue // per key, ascending by version
	version   uint64
	snapshots map[uint64]*Snapshot
	nextID    uint64
	now       Clock
	load      Loader
	fail      FailHook
	ttl       time.Duration
}

// NewStore creates a Store with the given options.
func NewStore(opts Options) *Store {
	s := &Store{
		data:      make(map[string][]versionedValue),
		snapshots: make(map[uint64]*Snapshot),
		now:       opts.Now,
		load:      opts.Load,
		fail:      opts.Fail,
		ttl:       opts.SnapshotTTL,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.load == nil {
		s.load = func(string) (string, bool, error) { return "", false, nil }
	}
	if s.fail == nil {
		s.fail = func(string) error { return nil }
	}
	return s
}

// Version returns the latest committed version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Begin opens a snapshot pinned at the current committed version.
// Every snapshot must be Closed to release its resources; snapshots
// that outlive SnapshotTTL are released automatically.
func (s *Store) Begin() (*Snapshot, error) {
	if err := s.fail("begin"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	snap := &Snapshot{
		store:   s,
		id:      s.nextID,
		version: s.version,
	}
	if s.ttl > 0 {
		snap.deadline = s.now().Add(s.ttl)
	}
	s.snapshots[snap.id] = snap
	return snap, nil
}

// Commit atomically publishes the whole batch as one new version.
// Either every op becomes visible at the returned version, or — on
// injected failure — nothing is applied and the version is unchanged.
func (s *Store) Commit(ops ...Op) (uint64, error) {
	if err := s.fail("commit"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	v := s.version
	for _, op := range ops {
		s.data[op.Key] = append(s.data[op.Key], versionedValue{
			version: v,
			value:   op.Value,
			deleted: op.Delete,
		})
	}
	return v, nil
}

// GetLatest reads keys at the newest committed version.
func (s *Store) GetLatest(keys ...string) (map[string]string, error) {
	if err := s.fail("get"); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readAt(s.version, keys), nil
}

// readAt resolves every key at the same version, giving a consistent
// multi-key view. Keys missing from memory fall back to the Loader.
// Callers must hold at least a read lock.
func (s *Store) readAt(version uint64, keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		chain := s.data[key]
		// Newest version not after the read version wins.
		for i := len(chain) - 1; i >= 0; i-- {
			if chain[i].version <= version {
				if !chain[i].deleted {
					out[key] = chain[i].value
				}
				break
			}
		}
		if _, ok := out[key]; ok || len(chain) > 0 {
			continue
		}
		if value, ok, err := s.load(key); err == nil && ok {
			out[key] = value
		}
	}
	return out
}

// ActiveSnapshots reports how many snapshots currently hold resources
// (open and not expired). Expired snapshots are released lazily here
// and on read, so the count is always accurate after a call.
func (s *Store) ActiveSnapshots() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseExpiredLocked()
	return len(s.snapshots)
}

// GC reclaims versions no active snapshot can still observe. The
// reclamation floor is the minimum version among active snapshots (or
// the latest version when none exist); for every key the newest version
// at or below the floor is kept so the floor itself stays readable.
// It returns the number of reclaimed versions.
func (s *Store) GC() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseExpiredLocked()

	floor := s.version
	for _, snap := range s.snapshots {
		if snap.version < floor {
			floor = snap.version
		}
	}

	reclaimed := 0
	for key, chain := range s.data {
		// Chain is ascending, so versions at or below the floor form a
		// prefix; keep only the newest of them plus everything newer.
		lastFloor := -1
		for i := range chain {
			if chain[i].version <= floor {
				lastFloor = i
			} else {
				break
			}
		}
		keep := make([]versionedValue, 0, len(chain))
		for i, vv := range chain {
			if i == lastFloor || vv.version > floor {
				keep = append(keep, vv)
			} else {
				reclaimed++
			}
		}
		if len(keep) == 0 {
			delete(s.data, key)
		} else {
			s.data[key] = keep
		}
	}
	return reclaimed
}

// releaseExpiredLocked drops expired snapshots from the registry.
// Callers must hold the write lock.
func (s *Store) releaseExpiredLocked() {
	if s.ttl <= 0 {
		return
	}
	now := s.now()
	for id, snap := range s.snapshots {
		if !now.Before(snap.deadline) {
			delete(s.snapshots, id)
		}
	}
}

// Snapshot is a consistent, multi-key read view pinned at one version.
type Snapshot struct {
	store    *Store
	id       uint64
	version  uint64
	deadline time.Time // zero when snapshots never expire

	mu     sync.Mutex
	closed bool
}

// Version returns the version this snapshot is pinned at.
func (s *Snapshot) Version() uint64 { return s.version }

// Get reads all keys at the snapshot's pinned version, so the result
// is always one consistent view: a mix of an older A and a newer B is
// impossible. Reading a closed snapshot fails with ErrSnapshotClosed;
// reading past the TTL fails with ErrSnapshotExpired and releases the
// snapshot.
func (s *Snapshot) Get(keys ...string) (map[string]string, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSnapshotClosed
	}
	s.mu.Unlock()

	if err := s.store.fail("get"); err != nil {
		return nil, err
	}

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.ttl > 0 && !s.store.now().Before(s.deadline) {
		delete(s.store.snapshots, s.id)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		return nil, ErrSnapshotExpired
	}
	return s.store.readAt(s.version, keys), nil
}

// Close releases the snapshot so GC can reclaim the versions it pinned.
// Close is idempotent.
func (s *Snapshot) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.store.mu.Lock()
	delete(s.store.snapshots, s.id)
	s.store.mu.Unlock()
	return nil
}
