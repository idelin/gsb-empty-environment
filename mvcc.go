// Package mvcc implements an in-memory snapshot-isolation MVCC store.
//
// Each transaction gets a monotonically increasing ID. Reads see the latest
// version committed before the transaction's snapshot; write-write conflicts
// are resolved with First-Committer-Wins and the conflicting transaction is
// aborted.
package mvcc

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrTxnNotActive is returned when operating on a committed/aborted txn.
	ErrTxnNotActive = errors.New("mvcc: transaction is not active")
	// ErrWriteConflict is returned when a write-write conflict aborts the txn.
	ErrWriteConflict = errors.New("mvcc: write-write conflict, transaction aborted")
	// ErrKeyNotFound is returned when no visible version exists for a key.
	ErrKeyNotFound = errors.New("mvcc: key not found")
)

// version is one committed or pending version of a key.
type version struct {
	value     string
	txnID     uint64 // writer transaction
	committed bool
}

// Store is a single-process MVCC key-value store.
type Store struct {
	mu        sync.Mutex
	nextID    uint64
	data      map[string][]*version // versions per key, oldest first
	committed map[uint64]bool       // set of committed transaction IDs
}

// NewStore creates a store whose first transaction ID is startID.
func NewStore(startID uint64) *Store {
	return &Store{
		nextID:    startID,
		data:      make(map[string][]*version),
		committed: make(map[uint64]bool),
	}
}

// Begin starts a new transaction with a snapshot of currently committed data.
func (s *Store) Begin() *Txn {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID
	s.nextID++

	snap := make(map[uint64]bool, len(s.committed))
	for cid := range s.committed {
		snap[cid] = true
	}
	return &Txn{
		store:    s,
		id:       id,
		snapshot: snap,
		writes:   make(map[string]string),
		active:   true,
	}
}

// Txn is a snapshot-isolation transaction.
type Txn struct {
	store    *Store
	id       uint64
	snapshot map[uint64]bool
	writes   map[string]string // pending write set
	active   bool
}

// ID returns the transaction ID.
func (t *Txn) ID() uint64 { return t.id }

// Get reads the newest version visible to this transaction's snapshot.
// The transaction's own pending writes take precedence.
func (t *Txn) Get(key string) (string, error) {
	if !t.active {
		return "", ErrTxnNotActive
	}
	if v, ok := t.writes[key]; ok {
		return v, nil
	}

	t.store.mu.Lock()
	defer t.store.mu.Unlock()

	versions := t.store.data[key]
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		if v.committed && t.snapshot[v.txnID] {
			return v.value, nil
		}
	}
	return "", ErrKeyNotFound
}

// Put stages a write. If another active transaction already has a pending
// write on the key, this transaction loses the write-write conflict and is
// aborted immediately.
func (t *Txn) Put(key, value string) error {
	if !t.active {
		return ErrTxnNotActive
	}

	t.store.mu.Lock()
	versions := t.store.data[key]
	for _, v := range versions {
		if !v.committed && v.txnID != t.id {
			t.store.mu.Unlock()
			t.rollback()
			return fmt.Errorf("%w: key %q has a pending write by txn %d", ErrWriteConflict, key, v.txnID)
		}
	}
	// Stage a pending version so other writers can detect the conflict.
	t.store.data[key] = append(versions, &version{value: value, txnID: t.id})
	t.store.mu.Unlock()

	t.writes[key] = value
	return nil
}

// Commit validates First-Committer-Wins and publishes the write set.
// If any key was committed by another transaction after this transaction's
// snapshot, this transaction is aborted.
func (t *Txn) Commit() error {
	if !t.active {
		return ErrTxnNotActive
	}

	t.store.mu.Lock()
	defer t.store.mu.Unlock()

	for key := range t.writes {
		for _, v := range t.store.data[key] {
			if v.committed && !t.snapshot[v.txnID] {
				t.rollbackLocked()
				return fmt.Errorf("%w: key %q was committed by txn %d after snapshot", ErrWriteConflict, key, v.txnID)
			}
		}
	}
	for key := range t.writes {
		for _, v := range t.store.data[key] {
			if !v.committed && v.txnID == t.id {
				v.committed = true
			}
		}
	}
	t.store.committed[t.id] = true
	t.active = false
	t.writes = nil
	return nil
}

// Abort rolls the transaction back, discarding its pending versions.
func (t *Txn) Abort() {
	if !t.active {
		return
	}
	t.rollback()
}

// rollback removes pending versions; caller must NOT hold the store lock.
func (t *Txn) rollback() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	t.rollbackLocked()
}

// rollbackLocked removes pending versions; caller must hold the store lock.
func (t *Txn) rollbackLocked() {
	for key := range t.writes {
		versions := t.store.data[key]
		kept := versions[:0]
		for _, v := range versions {
			if v.committed || v.txnID != t.id {
				kept = append(kept, v)
			}
		}
		if len(kept) == 0 {
			delete(t.store.data, key)
		} else {
			t.store.data[key] = kept
		}
	}
	t.active = false
	t.writes = nil
}
