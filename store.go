// Package memtx provides an in-memory transactional key-value store.
//
// Transactions read from a fixed commit snapshot taken at Begin and buffer
// their mutations in a private write set. Reads observe the transaction's
// own writes (read-your-writes). Range scans merge write-set inserts and
// deletes with the snapshot into a single ordered result. Pagination
// cursors are bound to the write-set revision: any subsequent mutation of
// the write set invalidates previously issued cursors.
package memtx

import (
	"errors"
	"sync"
)

// ErrConflict is returned by Commit when another transaction committed a
// write to a key in this transaction's write set after this transaction's
// snapshot was taken (first-committer-wins).
var ErrConflict = errors.New("memtx: write conflict")

// ErrStaleCursor is returned by Scan when the supplied cursor was issued
// before the most recent write-set mutation and can no longer be honored.
var ErrStaleCursor = errors.New("memtx: cursor invalidated by write-set change")

// ErrClosed is returned when an operation is attempted on a transaction
// that has already been committed or rolled back.
var ErrClosed = errors.New("memtx: transaction is closed")

// cell is one committed version of a key.
type cell struct {
	seq  uint64
	val  string
	tomb bool
}

// Store is the committed, multi-versioned state. The zero value is not
// usable; construct with NewStore.
type Store struct {
	mu   sync.RWMutex
	seq  uint64
	data map[string][]cell // per-key version chains, ascending seq
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{data: make(map[string][]cell)}
}

// Begin opens a new transaction pinned to the current commit sequence.
func (s *Store) Begin() *Txn {
	s.mu.RLock()
	snap := s.seq
	s.mu.RUnlock()
	return &Txn{
		store: s,
		snap:  snap,
		ws:    make(map[string]wsEntry),
	}
}

// snapshotGet returns the value of key visible at seq snap.
func (s *Store) snapshotGet(snap uint64, key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return visibleAt(s.data[key], snap)
}

// visibleAt resolves a version chain at the given snapshot sequence.
func visibleAt(chain []cell, snap uint64) (string, bool) {
	// Chains are short in practice; linear scan from newest is fine.
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].seq <= snap {
			if chain[i].tomb {
				return "", false
			}
			return chain[i].val, true
		}
	}
	return "", false
}

// snapshotEntries materializes every key visible at snap within
// [start, end). An empty end means unbounded.
func (s *Store) snapshotEntries(snap uint64, start, end string) []KV {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]KV, 0)
	for key, chain := range s.data {
		if key < start || (end != "" && key >= end) {
			continue
		}
		if val, ok := visibleAt(chain, snap); ok {
			out = append(out, KV{Key: key, Value: val})
		}
	}
	sortKVs(out)
	return out
}
