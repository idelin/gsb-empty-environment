// Package updatekv implements read-modify-write updates on top of a
// versioned, conditional-write store, with bounded conflict retries and
// side effects that are delivered exactly once, only after the write
// succeeds.
package updatekv

import (
	"context"
	"errors"
	"sync"
)

// ErrConflict is returned by Store.CompareAndSwap when the expected
// version does not match the current version of the record.
var ErrConflict = errors.New("updatekv: version conflict")

// Record is a value together with its optimistic-concurrency version.
type Record struct {
	Value   []byte
	Version uint64
}

// Store is the minimal storage contract: versioned reads and
// conditional writes. Implementations must return ErrConflict from
// CompareAndSwap when expectedVersion is stale.
type Store interface {
	Read(ctx context.Context, key string) (Record, error)
	CompareAndSwap(ctx context.Context, key string, value []byte, expectedVersion uint64) (newVersion uint64, err error)
}

// MemoryStore is a goroutine-safe in-memory Store, useful for tests and
// as a reference implementation.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]Record)}
}

// Put seeds or overwrites a key without a version check.
func (m *MemoryStore) Put(key string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.data[key]
	m.data[key] = Record{Value: value, Version: rec.Version + 1}
}

func (m *MemoryStore) Read(_ context.Context, key string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[key], nil
}

func (m *MemoryStore) CompareAndSwap(_ context.Context, key string, value []byte, expectedVersion uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.data[key]
	if rec.Version != expectedVersion {
		return 0, ErrConflict
	}
	rec.Value = value
	rec.Version++
	m.data[key] = rec
	return rec.Version, nil
}
