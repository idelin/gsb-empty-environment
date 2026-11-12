package netsec

import (
	"sync"
	"time"
)

// CounterStore persists the last accepted assertion counter for each key.
//
// Counter returns found=false for a key that has no recorded counter. In that
// case a Verifier treats the recorded counter as zero. Implementations must
// make CompareAndSwapCounter atomic: it must update the counter only when the
// stored value still equals previous, and it must report false instead of
// overwriting a concurrent update.
type CounterStore interface {
	Counter(keyID string) (counter uint32, found bool, err error)
	CompareAndSwapCounter(keyID string, previous, next uint32) (swapped bool, err error)
}

// ChallengeStore persists one-time challenges and their expiry times.
//
// ConsumeChallenge must atomically remove a challenge from the valid set and
// return nil exactly once for a live challenge. A missing challenge returns
// ErrChallengeNotFound, an expired challenge returns ErrChallengeExpired, and
// an already consumed challenge returns ErrChallengeReused. The challenge is
// consumed even when the rest of the assertion later fails, so a request
// cannot retry the same challenge.
type ChallengeStore interface {
	PutChallenge(challenge string, expiresAt time.Time) error
	ConsumeChallenge(challenge string, now time.Time) error
}

// Store combines the persistence required by a Verifier. A backend may use
// separate implementations by assigning Verifier.Counters and
// Verifier.Challenges directly.
type Store interface {
	CounterStore
	ChallengeStore
}

type challengeRecord struct {
	expiresAt time.Time
	used      bool
}

// MemoryStore is a concurrency-safe in-memory Store. It is suitable for
// tests, single-process services, and as a reference implementation. A
// production multi-instance backend should provide a shared Store backed by
// its database or cache so counters and challenges remain atomic across
// processes.
type MemoryStore struct {
	mu         sync.Mutex
	counters   map[string]uint32
	challenges map[string]challengeRecord
}

// NewMemoryStore creates an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		counters:   make(map[string]uint32),
		challenges: make(map[string]challengeRecord),
	}
}

// Counter returns the recorded counter for keyID.
func (s *MemoryStore) Counter(keyID string) (uint32, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()

	counter, found := s.counters[keyID]
	return counter, found, nil
}

// CompareAndSwapCounter atomically advances the counter only if the stored
// value is still previous.
func (s *MemoryStore) CompareAndSwapCounter(keyID string, previous, next uint32) (bool, error) {
	if next <= previous {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()

	current, found := s.counters[keyID]
	if !found {
		if previous != 0 {
			return false, nil
		}
		s.counters[keyID] = next
		return true, nil
	}
	if current != previous {
		return false, nil
	}
	s.counters[keyID] = next
	return true, nil
}

// PutChallenge records a challenge that is valid until expiresAt.
func (s *MemoryStore) PutChallenge(challenge string, expiresAt time.Time) error {
	if challenge == "" {
		return ErrEmptyChallenge
	}
	if expiresAt.IsZero() {
		return ErrInvalidChallengeExpiry
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()

	if _, exists := s.challenges[challenge]; exists {
		return ErrChallengeDuplicate
	}
	s.challenges[challenge] = challengeRecord{expiresAt: expiresAt}
	return nil
}

// ConsumeChallenge atomically invalidates a challenge and reports whether it
// was live when consumed.
func (s *MemoryStore) ConsumeChallenge(challenge string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()

	record, found := s.challenges[challenge]
	if !found {
		return ErrChallengeNotFound
	}
	if record.used {
		return ErrChallengeReused
	}

	// Mark the challenge used before checking time so an expired challenge
	// cannot be retried, and a second attempt is distinguishable from an
	// unknown challenge.
	record.used = true
	s.challenges[challenge] = record

	if !now.Before(record.expiresAt) {
		return ErrChallengeExpired
	}
	return nil
}

// init must be called while s.mu is held. It permits a zero-value
// MemoryStore to be used without panicking.
func (s *MemoryStore) init() {
	if s.counters == nil {
		s.counters = make(map[string]uint32)
	}
	if s.challenges == nil {
		s.challenges = make(map[string]challengeRecord)
	}
}
