package netsec

import (
	"fmt"
	"sync"
)

// RegisteredKey binds an opaque verification key to the only signature
// algorithm the verifier may use for it.
type RegisteredKey struct {
	Algorithm Algorithm
	Key       any
}

// KeyRegistry looks up a key by the keyid from Signature-Input. Implementations
// must return the algorithm from trusted key metadata, never from the request.
type KeyRegistry interface {
	Lookup(keyID string) (RegisteredKey, bool)
}

// MemoryKeyRegistry is a concurrency-safe in-memory KeyRegistry suitable for
// gateways that load and look up keys at startup.
type MemoryKeyRegistry struct {
	mu   sync.RWMutex
	keys map[string]RegisteredKey
}

// NewKeyRegistry creates an empty registry.
func NewKeyRegistry() *MemoryKeyRegistry {
	return &MemoryKeyRegistry{keys: make(map[string]RegisteredKey)}
}

// Register adds a key ID and binds it to alg. Unknown algorithms and keys that
// do not match alg are rejected. A key ID cannot be silently replaced.
func (r *MemoryKeyRegistry) Register(keyID string, alg Algorithm, key any) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrInvalidKey)
	}
	if keyID == "" {
		return fmt.Errorf("%w: empty key ID", ErrInvalidKey)
	}
	if err := validateKey(alg, key); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[string]RegisteredKey)
	}
	if _, exists := r.keys[keyID]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateKey, keyID)
	}
	r.keys[keyID] = RegisteredKey{Algorithm: alg, Key: key}
	return nil
}

// Lookup returns the registered key. The key ID is case-sensitive.
func (r *MemoryKeyRegistry) Lookup(keyID string) (RegisteredKey, bool) {
	if r == nil {
		return RegisteredKey{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[keyID]
	return key, ok
}
