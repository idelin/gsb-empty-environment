// Package update implements read-version/conditional-write updates with
// bounded conflict retries and exactly-once post-commit side-effect delivery.
package update

import (
	"context"
	"errors"
	"sync"
)

// ErrConflict is returned by CompareAndSwap when the expected version does
// not match the stored version.
var ErrConflict = errors.New("update: version conflict")

// Store exposes read-with-version and conditional write. It never supports
// blind writes: every mutation must go through CompareAndSwap.
type Store interface {
	// Read returns the current value and its version.
	Read(ctx context.Context, key string) (value []byte, version uint64, err error)
	// CompareAndSwap stores value only if the current version equals
	// expectedVersion, returning the new version. It returns ErrConflict
	// otherwise.
	CompareAndSwap(ctx context.Context, key string, value []byte, expectedVersion uint64) (uint64, error)
}

// MemoryStore is a goroutine-safe in-memory Store.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]entry
}

type entry struct {
	value   []byte
	version uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]entry)}
}

func (m *MemoryStore) Read(_ context.Context, key string) ([]byte, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	return append([]byte(nil), e.value...), e.version, nil
}

func (m *MemoryStore) CompareAndSwap(_ context.Context, key string, value []byte, expectedVersion uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	if e.version != expectedVersion {
		return 0, ErrConflict
	}
	next := e.version + 1
	m.entries[key] = entry{value: append([]byte(nil), value...), version: next}
	return next, nil
}
