package netsec

import (
	"context"
	"sync"
)

// Store is the persistence boundary for secret payloads. Protocol state lives
// in the Service; only the confidential bytes live behind this interface.
// Delete failures are first-class: they force the unknown terminal state.
type Store interface {
	// Put stores a payload. It returns ErrExists if the id is already used.
	Put(ctx context.Context, id string, payload []byte) error

	// Get returns a copy of the payload. ok is false when no blob exists.
	Get(ctx context.Context, id string) (payload []byte, ok bool, err error)

	// Delete destroys the payload blob. It must be idempotent: deleting an
	// already absent id returns nil.
	Delete(ctx context.Context, id string) error
}

// MemoryStore is an in-process Store for tests and single-node deployments.
// FailDelete injects deletion failures to exercise destroy-failure handling.
type MemoryStore struct {
	mu          sync.Mutex
	blobs       map[string][]byte
	deleteCalls int
	failDelete  func(id string) error
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{blobs: make(map[string][]byte)}
}

func (m *MemoryStore) Put(_ context.Context, id string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.blobs[id]; exists {
		return ErrExists
	}
	m.blobs[id] = cloneBytes(payload)
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	blob, ok := m.blobs[id]
	if !ok {
		return nil, false, nil
	}
	return cloneBytes(blob), true, nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	if m.failDelete != nil {
		if err := m.failDelete(id); err != nil {
			return err
		}
	}
	delete(m.blobs, id)
	return nil
}

// FailDelete installs a hook evaluated on every Delete. A non-nil error from
// the hook simulates a backend destruction failure and leaves the blob stored.
func (m *MemoryStore) FailDelete(hook func(id string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failDelete = hook
}

// DeleteCalls reports how many Delete attempts the backend has seen.
func (m *MemoryStore) DeleteCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteCalls
}

// Has reports whether the backend still physically holds the blob.
func (m *MemoryStore) Has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.blobs[id]
	return ok
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
