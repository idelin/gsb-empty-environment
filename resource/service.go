package resource

import "errors"

// Download-layer errors.
var (
	ErrForbidden = errors.New("resource: invalid signature")
	ErrNotFound  = errors.New("resource: object not found")
)

// Service wires authentication, caching, and the object store together so
// that all three consume the same immutable ID.
type Service struct {
	signer Signer
	cache  *Cache
	store  *Store
}

// NewService builds a Service from its three collaborators.
func NewService(signer Signer, cache *Cache, store *Store) *Service {
	return &Service{signer: signer, cache: cache, store: store}
}

// Sign issues a signature authorizing a fetch of id.
func (s *Service) Sign(id ID) string { return s.signer.Sign(id) }

// Fetch verifies the signature against the ID, serves from the cache when
// possible, and otherwise opens the object in the store. Every step keys
// off the same canonical identifier.
func (s *Service) Fetch(id ID, signature string) ([]byte, error) {
	if !s.signer.Verify(id, signature) {
		return nil, ErrForbidden
	}
	if data, ok := s.cache.Get(id); ok {
		return data, nil
	}
	data, ok := s.store.Get(id)
	if !ok {
		return nil, ErrNotFound
	}
	s.cache.Put(id, data)
	return data, nil
}
