// Package store is a minimal KV store guarded by a fencing token. Every write
// must carry the lease epoch the writer believes is current; the store only
// accepts tokens >= the highest epoch it has ever seen. This is the second
// line of defense against dual-writes: even if a partitioned primary wrongly
// believes it still holds the lease, its stale epoch is rejected.
package store

import (
	"errors"
	"sync"
)

// ErrStaleFencingToken is returned when a write carries an epoch older than
// the highest epoch the store has observed.
var ErrStaleFencingToken = errors.New("store: stale fencing token, write rejected")

type Store struct {
	mu    sync.RWMutex
	fence uint64
	data  map[string]string
}

func New() *Store {
	return &Store{data: make(map[string]string)}
}

// SetFence records the highest lease epoch observed. Monotonic: it never
// goes backwards.
func (s *Store) SetFence(epoch uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch > s.fence {
		s.fence = epoch
	}
}

// Fence returns the current fencing token.
func (s *Store) Fence() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fence
}

// Put writes k=v only if token is at least the current fence.
func (s *Store) Put(key, value string, token uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == 0 || token < s.fence {
		return ErrStaleFencingToken
	}
	s.data[key] = value
	return nil
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}
