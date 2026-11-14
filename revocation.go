package netsec

import "sync"

// RevocationStore reports whether a presented token has been revoked. A
// token is revoked either individually by its jti, or wholesale when its
// permission generation falls behind the minimum generation recorded for
// its service account (e.g. after a role change forces re-issuance).
type RevocationStore interface {
	// IsRevoked reports whether the token id has been individually revoked.
	IsRevoked(jti string) bool
	// MinGeneration returns the lowest permission generation still accepted
	// for the given namespace/service account pair. Zero accepts all.
	MinGeneration(namespace, serviceAccount string) int64
}

// InMemoryRevocationStore is a process-local RevocationStore suitable for
// services that receive revocation pushes; production deployments can
// substitute a store backed by a shared cache instead.
type InMemoryRevocationStore struct {
	mu            sync.RWMutex
	revokedIDs    map[string]struct{}
	minGeneration map[accountKey]int64
}

type accountKey struct {
	namespace      string
	serviceAccount string
}

// NewInMemoryRevocationStore returns an empty store.
func NewInMemoryRevocationStore() *InMemoryRevocationStore {
	return &InMemoryRevocationStore{
		revokedIDs:    make(map[string]struct{}),
		minGeneration: make(map[accountKey]int64),
	}
}

// Revoke marks a token id as revoked.
func (s *InMemoryRevocationStore) Revoke(jti string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokedIDs[jti] = struct{}{}
}

// SetMinGeneration invalidates every token for the account whose permission
// generation is lower than min.
func (s *InMemoryRevocationStore) SetMinGeneration(namespace, serviceAccount string, min int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minGeneration[accountKey{namespace, serviceAccount}] = min
}

// IsRevoked implements RevocationStore.
func (s *InMemoryRevocationStore) IsRevoked(jti string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.revokedIDs[jti]
	return ok
}

// MinGeneration implements RevocationStore.
func (s *InMemoryRevocationStore) MinGeneration(namespace, serviceAccount string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.minGeneration[accountKey{namespace, serviceAccount}]
}
