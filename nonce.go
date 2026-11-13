package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"sync"
)

// NonceStatus is the atomic result of trying to consume a c_nonce.
type NonceStatus uint8

const (
	// NonceAccepted means the nonce was the current, unused c_nonce and has now
	// been consumed.
	NonceAccepted NonceStatus = iota + 1
	// NonceMismatch means the nonce is not the current c_nonce for the scope.
	NonceMismatch
	// NonceReused means the current c_nonce was already consumed.
	NonceReused
)

// NonceStore tracks the current c_nonce per scope and consumes it exactly once.
//
// Consume must perform the equality check and the used-marking atomically. This
// is what makes concurrent replays of the same proof safe. Implementations backed
// by a shared database should use a conditional update (for example, an UPDATE
// whose WHERE clause requires the nonce to still be unused).
type NonceStore interface {
	// Issue creates a fresh c_nonce, makes it current for scope, and returns it.
	// A previously issued nonce for the scope stops being current.
	Issue(scope string) (string, error)
	// Consume atomically checks that nonce is current and unused for scope,
	// then marks it used.
	Consume(scope, nonce string) (NonceStatus, error)
}

type nonceEntry struct {
	value string
	used  bool
}

// MemoryNonceStore is a concurrency-safe in-memory NonceStore. It is suitable
// for a single issuer process. Deployments with multiple issuer instances or a
// restart requirement should provide a shared implementation of NonceStore.
type MemoryNonceStore struct {
	mu      sync.Mutex
	entries map[string]*nonceEntry
}

// NonceManager is an alias for MemoryNonceStore.
type NonceManager = MemoryNonceStore

// NewMemoryNonceStore creates an empty in-memory nonce store.
func NewMemoryNonceStore() *MemoryNonceStore {
	return &MemoryNonceStore{entries: make(map[string]*nonceEntry)}
}

// NewNonceManager is an alias for NewMemoryNonceStore.
func NewNonceManager() *MemoryNonceStore {
	return NewMemoryNonceStore()
}

// Issue implements NonceStore.
func (s *MemoryNonceStore) Issue(scope string) (string, error) {
	nonce, err := newNonce()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	if s.entries == nil {
		s.entries = make(map[string]*nonceEntry)
	}
	s.entries[scope] = &nonceEntry{value: nonce}
	s.mu.Unlock()

	return nonce, nil
}

// Consume implements NonceStore.
func (s *MemoryNonceStore) Consume(scope, nonce string) (NonceStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[scope]
	if !ok {
		return NonceMismatch, nil
	}
	if subtle.ConstantTimeCompare([]byte(entry.value), []byte(nonce)) != 1 {
		return NonceMismatch, nil
	}
	if entry.used {
		return NonceReused, nil
	}
	entry.used = true
	return NonceAccepted, nil
}

// Current returns the current c_nonce for scope. It is intended for tests and
// diagnostics; validation must use Consume so the check and use are atomic.
func (s *MemoryNonceStore) Current(scope string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[scope]
	if !ok {
		return "", false
	}
	return entry.value, true
}

// Retire invalidates the current c_nonce for scope. Subsequent proofs with that
// nonce are rejected as a mismatch.
func (s *MemoryNonceStore) Retire(scope string) {
	s.mu.Lock()
	delete(s.entries, scope)
	s.mu.Unlock()
}

func newNonce() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate c_nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
