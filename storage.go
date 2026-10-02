package distlock

import (
	"errors"
	"sync"
)

// ErrStaleFence is returned when a write carries a fencing token that is
// not greater than the highest token the resource has already seen.
var ErrStaleFence = errors.New("distlock: stale fencing token")

// Storage is a downstream resource guarded by fencing tokens. It only
// accepts writes whose token is strictly greater than any token seen so
// far, so a crashed-and-recovered former lock holder cannot corrupt state.
type Storage struct {
	mu       sync.Mutex
	maxToken uint64
	data     map[string]string
}

// NewStorage creates an empty fenced storage.
func NewStorage() *Storage {
	return &Storage{data: make(map[string]string)}
}

// Write applies key=value only if token fences out all previous writers.
func (s *Storage) Write(token uint64, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token <= s.maxToken {
		return ErrStaleFence
	}
	s.maxToken = token
	s.data[key] = value
	return nil
}

// Read returns the value for key.
func (s *Storage) Read(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}
