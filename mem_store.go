package netsec

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

// MemBlobStore is an in-memory BlobStore with the same idempotent/conflicting
// Put semantics as FileBlobStore. It is intended for tests.
type MemBlobStore struct {
	mu    sync.Mutex
	blobs map[string]map[string][]byte
}

// NewMemBlobStore creates an empty in-memory blob store.
func NewMemBlobStore() *MemBlobStore {
	return &MemBlobStore{blobs: make(map[string]map[string][]byte)}
}

func (m *MemBlobStore) bucket(objectID string, create bool) map[string][]byte {
	bucket := m.blobs[objectID]
	if bucket == nil && create {
		bucket = make(map[string][]byte)
		m.blobs[objectID] = bucket
	}
	return bucket
}

func (m *MemBlobStore) Put(ctx context.Context, objectID string, digest, envelope []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateObjectID(objectID); err != nil {
		return err
	}
	key := string(digest)
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(objectID, true)
	if existing, ok := bucket[key]; ok {
		if !bytes.Equal(existing, envelope) {
			return fmt.Errorf("%w: digest key already holds different envelope", ErrConflict)
		}
		return nil
	}
	bucket[key] = append([]byte(nil), envelope...)
	return nil
}

func (m *MemBlobStore) Get(ctx context.Context, objectID string, digest []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(objectID, false)
	if bucket == nil {
		return nil, fmt.Errorf("%w: unknown object %q", ErrNotFound, objectID)
	}
	raw, ok := bucket[string(digest)]
	if !ok {
		return nil, fmt.Errorf("%w: blob %x", ErrNotFound, digest)
	}
	return append([]byte(nil), raw...), nil
}

func (m *MemBlobStore) Has(ctx context.Context, objectID string, digest []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(objectID, false)
	if bucket == nil {
		return false, nil
	}
	_, ok := bucket[string(digest)]
	return ok, nil
}

// MemMetadataStore is an in-memory MetadataStore. It enforces compare-and-swap
// and refuses backwards pointer moves, mirroring FileMetadataStore.
type MemMetadataStore struct {
	mu       sync.Mutex
	pointers map[string]*Pointer
}

// NewMemMetadataStore creates an empty in-memory metadata store.
func NewMemMetadataStore() *MemMetadataStore {
	return &MemMetadataStore{pointers: make(map[string]*Pointer)}
}

func (m *MemMetadataStore) Get(ctx context.Context, objectID string) (*Pointer, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pointer, ok := m.pointers[objectID]
	if !ok {
		return nil, false, nil
	}
	copy := &Pointer{Version: pointer.Version, Digest: append([]byte(nil), pointer.Digest...)}
	return copy, true, nil
}

func (m *MemMetadataStore) CAS(ctx context.Context, objectID string, old, next *Pointer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next == nil {
		return fmt.Errorf("netsec: CAS requires a new pointer")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.pointers[objectID]
	var currentOrNil *Pointer
	if exists {
		currentOrNil = current
	}
	if !samePointer(currentOrNil, old) {
		return fmt.Errorf("%w: expected version %d, found %v", ErrConflict, expectedVersion(old), versionOrNil(currentOrNil))
	}
	if exists && next.Version <= current.Version {
		return fmt.Errorf("%w: pointer cannot move backwards (v%d -> v%d)", ErrConflict, current.Version, next.Version)
	}
	m.pointers[objectID] = &Pointer{Version: next.Version, Digest: append([]byte(nil), next.Digest...)}
	return nil
}
