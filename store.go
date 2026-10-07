// Package seq implements a segment-based unique ID allocator.
//
// The allocator reserves a contiguous segment of IDs by atomically
// compare-and-swapping a persisted high-water mark (State.SegmentEnd) in a
// Store, then hands out IDs from that segment purely in memory. Persistence
// happens once per segment instead of once per ID.
//
// Guarantees:
//   - An ID is issued only after the segment covering it has been
//     successfully persisted. If the flush fails, no ID is issued.
//   - Restarting skips the unused tail of the previously reserved segment
//     (gaps are allowed) but never reuses an ID that was already returned.
//   - Cross-process coordination is expressed only through the Store
//     interface's atomic CompareAndSwap, so any backend (file, DB, etcd)
//     that provides CAS can be plugged in.
package seq

import "sync"

// State is the persisted allocator state. SegmentEnd is the exclusive upper
// bound of all IDs reserved so far; Version is a fence for CAS.
type State struct {
	Version    uint64
	SegmentEnd uint64
}

// Store persists the allocator state and provides atomic compare-and-swap.
// CompareAndSwap must durably persist next and return true iff the currently
// persisted state equals expect; otherwise it must leave the state untouched
// and return false. A non-nil error means the flush failed and next may or
// may not have been persisted (callers must not assume either way).
type Store interface {
	Load() (State, error)
	CompareAndSwap(expect, next State) (bool, error)
}

// MemStore is an in-memory Store, useful for tests and single-process use.
type MemStore struct {
	mu sync.Mutex
	st State
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{} }

// Load implements Store.
func (m *MemStore) Load() (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, nil
}

// CompareAndSwap implements Store.
func (m *MemStore) CompareAndSwap(expect, next State) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st != expect {
		return false, nil
	}
	m.st = next
	return true, nil
}
