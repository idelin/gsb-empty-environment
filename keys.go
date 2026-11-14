package netsec

import (
	"crypto"
	"fmt"
	"sync"
)

// KeySet resolves the public key used to verify a token signature. The
// validator never fetches keys itself; callers plug in a set sourced from
// the cluster's trusted issuer metadata.
type KeySet interface {
	// Key returns the public key for the given key id and algorithm, or an
	// error if no suitable key is known.
	Key(kid, alg string) (crypto.PublicKey, error)
}

// StaticKeySet is an in-memory KeySet keyed by key id. It is safe for
// concurrent use and supports rotation via Add and Remove.
type StaticKeySet struct {
	mu   sync.RWMutex
	keys map[string]crypto.PublicKey
}

// NewStaticKeySet returns an empty StaticKeySet.
func NewStaticKeySet() *StaticKeySet {
	return &StaticKeySet{keys: make(map[string]crypto.PublicKey)}
}

// Add registers or replaces the public key for a key id.
func (s *StaticKeySet) Add(kid string, key crypto.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[kid] = key
}

// Remove drops a key id, e.g. after issuer key rotation.
func (s *StaticKeySet) Remove(kid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, kid)
}

// Key implements KeySet.
func (s *StaticKeySet) Key(kid, _ string) (crypto.PublicKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	return key, nil
}
