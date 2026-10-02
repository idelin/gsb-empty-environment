package ss2pl

import "sync"

// Store is a minimal in-memory key/value store of int64 values. Writes are
// applied in place; transactions keep an undo log so aborts (including
// crash recovery) can restore the previous image.
type Store struct {
	mu   sync.Mutex
	data map[string]int64
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{data: make(map[string]int64)}
}

// Get returns the value for key and whether it exists.
func (s *Store) Get(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// Set writes val and returns the previous image for the undo log.
func (s *Store) Set(key string, val int64) (old int64, existed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, existed = s.data[key]
	s.data[key] = val
	return old, existed
}

// Delete removes key (used to undo an insert).
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

// Snapshot returns a copy of the whole store.
func (s *Store) Snapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}
