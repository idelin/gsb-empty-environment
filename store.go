package netsec

import "sync"

// Store persists committed nodes, keyed by their digest. Commit writes every
// new node before swapping the root; a failed Put aborts the commit and the
// published tree keeps its old root.
type Store interface {
	Put(d Digest, blob []byte) error
}

// MemoryStore is the default in-memory Store.
type MemoryStore struct {
	mu    sync.Mutex
	nodes map[Digest][]byte
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nodes: make(map[Digest][]byte)}
}

// Put stores blob under d.
func (s *MemoryStore) Put(d Digest, blob []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(blob))
	copy(cp, blob)
	s.nodes[d] = cp
	return nil
}

// Get returns the blob stored under d.
func (s *MemoryStore) Get(d Digest) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, ok := s.nodes[d]
	return blob, ok
}

// Len returns how many nodes the store holds.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.nodes)
}
