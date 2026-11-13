package netsec

import "sync"

// ChainKeyStore is the host-provided secure storage for chain key material.
// The state machine never retains raw chain keys; it only holds Handles.
// Implementations must be safe for concurrent use.
type ChainKeyStore interface {
	// Put stores key under handle, replacing any previous value.
	Put(handle Handle, key []byte) error
	// Get returns a copy of the key stored under handle.
	Get(handle Handle) ([]byte, error)
	// Delete removes the key stored under handle. Deleting a missing handle
	// must succeed.
	Delete(handle Handle) error
}

// ErrKeyNotFound is returned by a ChainKeyStore when a handle is unknown.
var ErrKeyNotFound = keyNotFoundError{}

type keyNotFoundError struct{}

func (keyNotFoundError) Error() string { return "netsec: chain key handle not found" }

// MemoryKeyStore is an in-memory ChainKeyStore intended for tests and for
// hosts that back chain keys with an external KMS elsewhere. It copies all
// key material on Put and Get so callers cannot mutate stored keys in place.
type MemoryKeyStore struct {
	mu   sync.Mutex
	keys map[Handle][]byte
}

// NewMemoryKeyStore returns an empty in-memory chain key store.
func NewMemoryKeyStore() *MemoryKeyStore {
	return &MemoryKeyStore{keys: make(map[Handle][]byte)}
}

// Put implements ChainKeyStore.
func (s *MemoryKeyStore) Put(handle Handle, key []byte) error {
	if len(key) != 32 {
		return ErrInvalidChainKey
	}
	stored := make([]byte, len(key))
	copy(stored, key)

	s.mu.Lock()
	s.keys[handle] = stored
	s.mu.Unlock()
	return nil
}

// Get implements ChainKeyStore.
func (s *MemoryKeyStore) Get(handle Handle) ([]byte, error) {
	s.mu.Lock()
	key, ok := s.keys[handle]
	s.mu.Unlock()
	if !ok {
		return nil, ErrKeyNotFound
	}
	out := make([]byte, len(key))
	copy(out, key)
	return out, nil
}

// Delete implements ChainKeyStore.
func (s *MemoryKeyStore) Delete(handle Handle) error {
	s.mu.Lock()
	delete(s.keys, handle)
	s.mu.Unlock()
	return nil
}
