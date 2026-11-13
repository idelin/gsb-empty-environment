package netsec

import (
	"bytes"
	"fmt"
	"sync"
)

// MemoryRecordStore is a thread-safe in-memory AtomicRecordStore. It is useful
// for tests and small hosts; production storage must provide equivalent atomic
// replacement semantics.
type MemoryRecordStore struct {
	mutex  sync.Mutex
	record *Record
}

// NewMemoryRecordStore creates a store holding a deep copy of initial. initial
// may be nil to create an empty store.
func NewMemoryRecordStore(initial *Record) *MemoryRecordStore {
	return &MemoryRecordStore{record: initial.Clone()}
}

// Load returns a deep copy of the current record.
func (store *MemoryRecordStore) Load() (*Record, error) {
	if store == nil {
		return nil, ErrNoRecord
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.record == nil {
		return nil, ErrNoRecord
	}
	return store.record.Clone(), nil
}

// CompareAndSwap atomically replaces old with next when old is current.
func (store *MemoryRecordStore) CompareAndSwap(old, next *Record) (bool, error) {
	if store == nil || old == nil || next == nil {
		return false, fmt.Errorf("%w: nil record", ErrMalformedRecord)
	}

	if err := validateRecord(old); err != nil {
		return false, err
	}
	if err := validateRecord(next); err != nil {
		return false, err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.record == nil {
		return false, nil
	}
	if !equalRecords(store.record, old) {
		return false, nil
	}
	store.record = next.Clone()
	return true, nil
}

func equalRecords(left, right *Record) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Version == right.Version &&
		left.KeyID == right.KeyID &&
		left.Commit == right.Commit &&
		bytes.Equal(left.Nonce, right.Nonce) &&
		bytes.Equal(left.Ciphertext, right.Ciphertext)
}
