package netsec

import (
	"bytes"
	"fmt"
	"sync"
)

// RepositoryID is an opaque, caller-assigned repository identity. It is never
// inferred from the current filesystem or from a remote URL.
type RepositoryID string

func (id RepositoryID) String() string {
	return string(id)
}

func validateRepositoryID(id RepositoryID) error {
	if id == "" {
		return fmt.Errorf("%w: empty repository identity", ErrInvalidPolicy)
	}
	if bytes.IndexByte([]byte(id), 0) >= 0 {
		return fmt.Errorf("%w: NUL in repository identity", ErrInvalidPolicy)
	}
	return nil
}

type objectKey struct {
	repository RepositoryID
	digest     Digest
}

// ObjectStore is the immutable object database supplied by the host. The
// builder only calls Get. A returned object must be content-addressed by
// digest; the builder verifies this instead of trusting the store.
type ObjectStore interface {
	Get(repository RepositoryID, digest Digest) (Object, error)
}

// MemoryStore is an in-memory ObjectStore intended for tests and for hosts
// that load an immutable snapshot before producing a source closure.
//
// It is safe for concurrent use. Put and PutRaw never overwrite an existing
// key with different content.
type MemoryStore struct {
	mu      sync.RWMutex
	objects map[objectKey]Object
}

// NewMemoryStore creates an empty in-memory object store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[objectKey]Object)}
}

// Get implements ObjectStore.
func (s *MemoryStore) Get(repository RepositoryID, digest Digest) (Object, error) {
	if s == nil {
		return Object{}, fmt.Errorf("%w: nil memory store", ErrObjectNotFound)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[objectKey{repository: repository, digest: digest}]
	if !ok {
		return Object{}, fmt.Errorf("%w: repository=%q digest=%s", ErrObjectNotFound, repository, digest)
	}
	return object.Clone(), nil
}

// Has reports whether a key is present.
func (s *MemoryStore) Has(repository RepositoryID, digest Digest) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objects[objectKey{repository: repository, digest: digest}]
	return ok
}

// Put stores an object under its recomputed digest.
func (s *MemoryStore) Put(repository RepositoryID, object Object) (Digest, error) {
	digest, err := ObjectDigest(object)
	if err != nil {
		return ZeroDigest, err
	}
	if err := s.PutRaw(repository, digest, object); err != nil {
		return ZeroDigest, err
	}
	return digest, nil
}

// PutRaw stores an object under a caller-chosen digest without verifying that
// the digest matches the payload. It exists so tests and hostile-store
// simulations can exercise digest verification. Production hosts should use
// Put.
func (s *MemoryStore) PutRaw(repository RepositoryID, digest Digest, object Object) error {
	if s == nil {
		return fmt.Errorf("%w: nil memory store", ErrInvalidObject)
	}
	if err := validateRepositoryID(repository); err != nil {
		return err
	}
	if digest.IsZero() {
		return fmt.Errorf("%w: zero digest", ErrInvalidObject)
	}
	key := objectKey{repository: repository, digest: digest}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = make(map[objectKey]Object)
	}
	if existing, ok := s.objects[key]; ok {
		if existing.Type == object.Type && bytes.Equal(existing.Data, object.Data) {
			return nil
		}
		return fmt.Errorf("%w: repository=%q digest=%s", ErrObjectExists, repository, digest)
	}
	s.objects[key] = object.Clone()
	return nil
}

// AddBlob stores blob content and returns its digest.
func (s *MemoryStore) AddBlob(repository RepositoryID, content []byte) (Digest, error) {
	return s.Put(repository, Object{Type: KindBlob, Data: append([]byte(nil), content...)})
}

// AddTree stores a canonical tree and returns its digest.
func (s *MemoryStore) AddTree(repository RepositoryID, entries []TreeEntry) (Digest, error) {
	object, err := NewTree(entries)
	if err != nil {
		return ZeroDigest, err
	}
	return s.Put(repository, object)
}

// AddCommit stores a canonical commit and returns its digest.
func (s *MemoryStore) AddCommit(repository RepositoryID, tree Digest, parents ...Digest) (Digest, error) {
	object := NewCommit(tree, parents)
	if _, err := ParseCommit(object); err != nil {
		return ZeroDigest, err
	}
	return s.Put(repository, object)
}

// Len returns the number of stored objects.
func (s *MemoryStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.objects)
}
