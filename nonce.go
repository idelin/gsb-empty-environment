package netsec

import (
	"crypto/sha256"
	"sync"
)

type nonceKey [sha256.Size]byte

// NonceStore remembers server nonces consumed by successful certificate
// activations. It is safe for concurrent use and can be shared by multiple
// Guards.
type NonceStore struct {
	mu   sync.Mutex
	used map[nonceKey]struct{}
}

// NewNonceStore returns an empty nonce store.
func NewNonceStore() *NonceStore {
	return &NonceStore{used: make(map[nonceKey]struct{})}
}

// Consume atomically marks nonce as used. It returns false if the nonce is
// empty or has already been consumed.
func (s *NonceStore) Consume(nonce []byte) bool {
	if s == nil {
		return false
	}
	key, ok := makeNonceKey(nonce)
	if !ok {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.used == nil {
		s.used = make(map[nonceKey]struct{})
	}
	if _, exists := s.used[key]; exists {
		return false
	}
	s.used[key] = struct{}{}
	return true
}

// IsUsed reports whether nonce has already been consumed.
func (s *NonceStore) IsUsed(nonce []byte) bool {
	if s == nil {
		return false
	}
	key, ok := makeNonceKey(nonce)
	if !ok {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.used[key]
	return exists
}

// Len returns the number of consumed nonces.
func (s *NonceStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.used)
}

// Reset clears all consumed nonces. A server must use this only when its
// nonce namespace has also been reset (for example on a process restart).
func (s *NonceStore) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used = make(map[nonceKey]struct{})
}

func makeNonceKey(nonce []byte) (nonceKey, bool) {
	if len(nonce) == 0 {
		return nonceKey{}, false
	}
	return sha256.Sum256(nonce), true
}
