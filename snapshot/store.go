// Package snapshot provides a low-pause, copy-on-write KV store with
// incremental (delta) snapshots and checkpoint archiving. All persisted
// files are encoded with the local wire/proto codecs - no external
// dependencies.
//
// Design notes:
//   - State is layered: immutable base + pending overlay (being merged into
//     a checkpoint) + active overlay. Reads never block writers for longer
//     than two map lookups; writes only clone the small active overlay.
//   - Checkpointing rotates layers under a short lock, merges the captured
//     layers and writes the archive outside the lock, then installs the
//     merged base. Writer pause is O(1) instead of O(state size).
//   - A versioned journal records every mutation since the last checkpoint,
//     enabling incremental delta archives; it is compacted on checkpoint.
package snapshot

import (
	"errors"
	"sync"
)

// Mutation is a single versioned state change.
type Mutation struct {
	Key     string
	Value   []byte // nil when Deleted
	Deleted bool
	Version uint64
}

var (
	// ErrCompacted is returned by DeltaSince when the requested version has
	// already been folded into a checkpoint and is no longer available.
	ErrCompacted = errors.New("snapshot: requested version has been compacted")
	// ErrCheckpointInProgress is returned when a checkpoint is already running.
	ErrCheckpointInProgress = errors.New("snapshot: checkpoint already in progress")
)

// Store is a concurrent, versioned key-value state store.
type Store struct {
	mu            sync.RWMutex
	base          map[string][]byte // immutable snapshot at baseVersion
	pending       map[string][]byte // overlay captured by an in-flight checkpoint
	overlay       map[string][]byte // active overlay, replaced copy-on-write
	journal       []Mutation        // mutations with Version > baseVersion
	version       uint64
	baseVersion   uint64
	checkpointing bool
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		base:    make(map[string][]byte),
		overlay: make(map[string][]byte),
	}
}

// NewStoreFrom builds a store whose base is the given state at the given
// version. The map is not copied and must not be mutated by the caller.
func NewStoreFrom(state map[string][]byte, version uint64) *Store {
	s := NewStore()
	s.base = state
	s.version = version
	s.baseVersion = version
	return s
}

// Version returns the latest committed mutation version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// BaseVersion returns the version folded into the immutable base.
func (s *Store) BaseVersion() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.baseVersion
}

// Get looks up a key across overlay, pending and base layers.
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.overlay[key]; ok {
		return v, v != nil
	}
	if v, ok := s.pending[key]; ok {
		return v, v != nil
	}
	v, ok := s.base[key]
	return v, ok
}

// Len returns the number of live keys.
func (s *Store) Len() int {
	state, _ := s.Snapshot()
	return len(state)
}

// Put sets a key and returns the new version. The value is copied.
func (s *Store) Put(key string, val []byte) uint64 {
	cp := make([]byte, len(val))
	copy(cp, val)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	next := make(map[string][]byte, len(s.overlay)+1)
	for k, v := range s.overlay {
		next[k] = v
	}
	next[key] = cp
	s.overlay = next
	s.journal = append(s.journal, Mutation{Key: key, Value: cp, Version: s.version})
	return s.version
}

// Del removes a key and returns the new version.
func (s *Store) Del(key string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	next := make(map[string][]byte, len(s.overlay)+1)
	for k, v := range s.overlay {
		next[k] = v
	}
	next[key] = nil // tombstone
	s.overlay = next
	s.journal = append(s.journal, Mutation{Key: key, Deleted: true, Version: s.version})
	return s.version
}

// DeltaSince returns every mutation with Version > v plus the current
// version. It fails with ErrCompacted if v predates the base version.
func (s *Store) DeltaSince(v uint64) ([]Mutation, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v < s.baseVersion {
		return nil, 0, ErrCompacted
	}
	var out []Mutation
	for _, m := range s.journal {
		if m.Version > v {
			out = append(out, m)
		}
	}
	return out, s.version, nil
}

// Snapshot returns a consistent full copy of the state and its version.
func (s *Store) Snapshot() (map[string][]byte, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]byte, len(s.base)+len(s.pending)+len(s.overlay))
	for k, v := range s.base {
		out[k] = v
	}
	applyOverlay(out, s.pending)
	applyOverlay(out, s.overlay)
	return out, s.version
}

// JournalLen reports how many uncompacted mutations are buffered.
func (s *Store) JournalLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.journal)
}

// PendingMerge reports whether a checkpoint merge is in flight.
func (s *Store) PendingMerge() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkpointing
}

// applyOverlay folds an overlay into dst; nil values are tombstones.
func applyOverlay(dst, overlay map[string][]byte) {
	for k, v := range overlay {
		if v == nil {
			delete(dst, k)
		} else {
			dst[k] = v
		}
	}
}

// MergeLayers produces the merged view of base+overlay without mutating
// either. Exported for use by the Archiver.
func MergeLayers(base, overlay map[string][]byte) map[string][]byte {
	merged := make(map[string][]byte, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	applyOverlay(merged, overlay)
	return merged
}

// beginCheckpoint rotates the active overlay into the pending slot and
// returns the captured layers. The caller must later call either
// finishCheckpoint or abortCheckpoint.
func (s *Store) beginCheckpoint() (base, overlay map[string][]byte, version uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkpointing {
		return nil, nil, 0, ErrCheckpointInProgress
	}
	s.checkpointing = true
	base, overlay, version = s.base, s.overlay, s.version
	s.pending = s.overlay
	s.overlay = make(map[string][]byte)
	return base, overlay, version, nil
}

// finishCheckpoint installs the merged base and compacts the journal.
func (s *Store) finishCheckpoint(merged map[string][]byte, version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.base = merged
	s.pending = nil
	s.baseVersion = version
	kept := s.journal[:0]
	for _, m := range s.journal {
		if m.Version > version {
			kept = append(kept, m)
		}
	}
	s.journal = kept
	s.checkpointing = false
}

// abortCheckpoint rolls back a failed checkpoint, restoring the captured
// overlay ahead of any mutations that arrived during the attempt.
func (s *Store) abortCheckpoint() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Restore pending entries ahead of newer overlay entries, keeping
	// tombstones intact (a delete must still shadow the base layer).
	restored := make(map[string][]byte, len(s.pending)+len(s.overlay))
	for k, v := range s.pending {
		restored[k] = v
	}
	for k, v := range s.overlay {
		restored[k] = v
	}
	s.overlay = restored
	s.pending = nil
	s.checkpointing = false
}
