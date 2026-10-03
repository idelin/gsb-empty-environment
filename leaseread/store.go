package leaseread

import (
	"errors"
	"sync"
)

// ErrKeyNotFound is returned when a key does not exist in the store.
var ErrKeyNotFound = errors.New("leaseread: key not found")

// KVStore is the replicated state machine on the leader. In a real database
// this is the applied Raft log; here it is a versioned in-memory map.
type KVStore struct {
	mu           sync.RWMutex
	data         map[string][]byte
	appliedIndex uint64
}

func NewKVStore() *KVStore {
	return &KVStore{data: make(map[string][]byte)}
}

// Put applies a committed write at the given log index.
func (s *KVStore) Put(index uint64, key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	s.data[key] = cp
	if index > s.appliedIndex {
		s.appliedIndex = index
	}
}

// Get reads the latest applied value for key.
func (s *KVStore) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

// AppliedIndex is the highest log index applied to the state machine.
func (s *KVStore) AppliedIndex() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appliedIndex
}
