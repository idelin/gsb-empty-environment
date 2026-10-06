package cachemodel

import (
	"fmt"
	"sync"
)

// MemStore is an exact in-memory Store.
type MemStore struct {
	mu   sync.Mutex
	data []byte
}

func NewMemStore(size int) *MemStore {
	return &MemStore{data: make([]byte, size)}
}

func (s *MemStore) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < 0 || off+int64(len(p)) > int64(len(s.data)) {
		return 0, fmt.Errorf("cachemodel: store write out of range (off %d, len %d)", off, len(p))
	}
	copy(s.data[off:], p)
	return len(p), nil
}

func (s *MemStore) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < 0 || off+int64(len(p)) > int64(len(s.data)) {
		return 0, fmt.Errorf("cachemodel: store read out of range (off %d, len %d)", off, len(p))
	}
	copy(p, s.data[off:])
	return len(p), nil
}

// Bytes returns a copy of the store contents.
func (s *MemStore) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.data))
	copy(out, s.data)
	return out
}

// ShortStore wraps a Store and truncates every write to MaxChunk bytes,
// simulating a device that only ever completes partial writes.
type ShortStore struct {
	Inner    Store
	MaxChunk int
}

func (s *ShortStore) WriteAt(p []byte, off int64) (int, error) {
	if len(p) > s.MaxChunk {
		p = p[:s.MaxChunk]
	}
	return s.Inner.WriteAt(p, off)
}

func (s *ShortStore) ReadAt(p []byte, off int64) (int, error) {
	return s.Inner.ReadAt(p, off)
}

// FailStore allows Budget bytes through, then fails every further write.
type FailStore struct {
	Inner  Store
	Budget int64
	Err    error

	mu sync.Mutex
}

func (s *FailStore) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Budget <= 0 {
		return 0, s.Err
	}
	truncated := false
	if int64(len(p)) > s.Budget {
		p = p[:s.Budget]
		truncated = true
	}
	n, err := s.Inner.WriteAt(p, off)
	s.Budget -= int64(n)
	if err == nil && truncated {
		err = s.Err
	}
	return n, err
}

func (s *FailStore) ReadAt(p []byte, off int64) (int, error) {
	return s.Inner.ReadAt(p, off)
}

// GateStore blocks writes until Release is closed. Started is closed when
// the first write arrives, so tests can interleave actions while a
// writeback is in flight.
type GateStore struct {
	Inner   Store
	Started chan struct{}
	Release <-chan struct{}

	once sync.Once
}

func NewGateStore(inner Store, release <-chan struct{}) *GateStore {
	return &GateStore{Inner: inner, Started: make(chan struct{}), Release: release}
}

func (s *GateStore) WriteAt(p []byte, off int64) (int, error) {
	s.once.Do(func() { close(s.Started) })
	<-s.Release
	return s.Inner.WriteAt(p, off)
}

func (s *GateStore) ReadAt(p []byte, off int64) (int, error) {
	return s.Inner.ReadAt(p, off)
}
