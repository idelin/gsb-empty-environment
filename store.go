package main

import "errors"

// Store is the durable record of the highest fence number ever issued.
// The lease manager persists the fence BEFORE handing out a lease
// (write-ahead), so a recovered manager can never issue a fence lower
// than one already granted before a crash.
type Store struct {
	fence   uint64
	failing bool // injected persistence failure
}

func NewStore() *Store { return &Store{} }

var ErrStoreUnavailable = errors.New("persistent store unavailable")

// SaveFence durably records the fence. It only ever moves forward.
func (s *Store) SaveFence(fence uint64) error {
	if s.failing {
		return ErrStoreUnavailable
	}
	if fence > s.fence {
		s.fence = fence
	}
	return nil
}

// LoadFence returns the highest durably recorded fence (0 if none).
func (s *Store) LoadFence() uint64 { return s.fence }

// SetFailing injects or clears a persistence failure (simulator use).
func (s *Store) SetFailing(failing bool) { s.failing = failing }
