package netsec

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// MemoryStore is an in-memory BlockStore with content-defined
// deduplication. A block's bytes are persisted once per distinct digest;
// every Put of an already stored digest shares the block and only bumps its
// reference count.
//
// It is suitable as the default local backend and as a deterministic test
// double: PhysicalBlocks counts distinct stored blocks and PhysicalWrites
// counts how often bytes were actually written.
type MemoryStore struct {
	mu       sync.Mutex
	blocks   map[Digest]*storedBlock
	refCount map[Reference]int
	writes   int
}

type storedBlock struct {
	digest Digest
	data   []byte
}

// readSeekNopCloser adapts *bytes.Reader to the io.ReadCloser interface.
type readSeekNopCloser struct {
	*bytes.Reader
}

func (readSeekNopCloser) Close() error { return nil }

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		blocks:   make(map[Digest]*storedBlock),
		refCount: make(map[Reference]int),
	}
}

// canonicalReference derives a stable reference for a digest so repeated
// Puts of identical content resolve to the same handle.
func canonicalReference(digest Digest) Reference {
	return Reference("blk-" + string(digest))
}

// Put implements BlockStore. Data is copied before storage so later mutation
// of the caller's slice cannot corrupt the block.
func (s *MemoryStore) Put(_ context.Context, digest Digest, data []byte) (Reference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ref := canonicalReference(digest)
	if _, exists := s.blocks[digest]; !exists {
		copied := make([]byte, len(data))
		copy(copied, data)
		s.blocks[digest] = &storedBlock{digest: digest, data: copied}
		s.writes++
	}
	s.refCount[ref]++
	return ref, nil
}

// Open implements BlockStore.
func (s *MemoryStore) Open(_ context.Context, ref Reference) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	block, ok := s.blockForRef(ref)
	if !ok {
		return nil, ErrNotFound
	}
	return readSeekNopCloser{bytes.NewReader(block.data)}, nil
}

// Release implements BlockStore. The block is reclaimed once its last
// reference is released.
func (s *MemoryStore) Release(_ context.Context, ref Reference) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.refCount[ref]
	if !ok || n <= 0 {
		return ErrNotFound
	}
	n--
	if n == 0 {
		delete(s.refCount, ref)
		if block, ok := s.blockForRef(ref); ok {
			delete(s.blocks, block.digest)
		}
		return nil
	}
	s.refCount[ref] = n
	return nil
}

func (s *MemoryStore) blockForRef(ref Reference) (*storedBlock, bool) {
	for digest := range s.blocks {
		if canonicalReference(digest) == ref {
			return s.blocks[digest], true
		}
	}
	return nil, false
}

// PhysicalBlocks returns the number of distinct blocks currently holding
// bytes in the store.
func (s *MemoryStore) PhysicalBlocks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blocks)
}

// PhysicalWrites returns how many times bytes were actually persisted. A
// deduplicated Put does not increment this counter.
func (s *MemoryStore) PhysicalWrites() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

// ReferenceCount returns the current reference count for ref.
func (s *MemoryStore) ReferenceCount(ref Reference) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refCount[ref]
}
