package seq

import (
	"context"
	"sync"
)

// CrashPoint identifies where a fault is injected around a reservation
// flush.
type CrashPoint int

const (
	// CrashBeforeFlush fails the compare-and-swap before the write is
	// applied: the reservation is lost and the caller sees an error.
	CrashBeforeFlush CrashPoint = iota
	// CrashAfterFlush applies the write and then reports an error: the
	// reservation is persisted but the caller cannot know that.
	CrashAfterFlush
)

// FaultStore wraps a Store and injects failures for testing crash
// behaviour. Hooks are evaluated under a lock so tests can change them
// between operations without races.
type FaultStore struct {
	inner Store

	mu sync.Mutex
	// BeforeFlush, when non-nil, runs before a CAS write is applied.
	// A non-nil error aborts the write (crash before flush).
	BeforeFlush func(key string, expected, next uint64) error
	// AfterFlush, when non-nil, runs after a CAS write was applied.
	// A non-nil error is returned to the caller even though the write
	// went through (crash after flush / lost acknowledgement).
	AfterFlush func(key string, expected, next uint64) error
	// LoadErr, when non-nil, fails every MaxReserved call.
	LoadErr error
}

// NewFaultStore wraps inner with fault injection.
func NewFaultStore(inner Store) *FaultStore {
	return &FaultStore{inner: inner}
}

// MaxReserved implements Store.
func (s *FaultStore) MaxReserved(ctx context.Context, key string) (uint64, error) {
	s.mu.Lock()
	loadErr := s.LoadErr
	s.mu.Unlock()
	if loadErr != nil {
		return 0, loadErr
	}
	return s.inner.MaxReserved(ctx, key)
}

// CompareAndSwapMaxReserved implements Store, injecting faults around
// the inner atomic write.
func (s *FaultStore) CompareAndSwapMaxReserved(ctx context.Context, key string, expected, next uint64) error {
	s.mu.Lock()
	before := s.BeforeFlush
	s.mu.Unlock()
	if before != nil {
		if err := before(key, expected, next); err != nil {
			return err
		}
	}
	if err := s.inner.CompareAndSwapMaxReserved(ctx, key, expected, next); err != nil {
		return err
	}
	s.mu.Lock()
	after := s.AfterFlush
	s.mu.Unlock()
	if after != nil {
		return after(key, expected, next)
	}
	return nil
}

// SetFault atomically replaces the fault hooks. Pass nil hooks to
// restore a healthy store.
func (s *FaultStore) SetFault(before, after func(key string, expected, next uint64) error, loadErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.BeforeFlush = before
	s.AfterFlush = after
	s.LoadErr = loadErr
}
