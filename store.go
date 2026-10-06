// Package dedup implements a deduplicating result store.
//
// Results of processed operations are stored under a dedup key so that
// later operations can reference them instead of re-executing business
// logic. A result entity is evicted only when BOTH conditions hold:
// its retention period has expired and no active references remain in
// the reference ledger.
//
// Result entities and anti-duplicate tombstones have different
// lifecycles: when a result is evicted, a tombstone is left behind to
// keep suppressing duplicate deliveries. A tombstone is dropped only
// after the source confirms a watermark at or beyond the position the
// tombstone recorded, i.e. the source guarantees the event can no
// longer be redelivered.
package dedup

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Position identifies an event's location in a source stream (e.g. a
// partition offset or sequence number). Watermarks are compared per
// source.
type Position struct {
	Source string
	Offset uint64
}

// Status describes the outcome of a Do call.
type Status int

const (
	// StatusExecuted means the business function ran and its result
	// was stored.
	StatusExecuted Status = iota
	// StatusReplayed means a live result entity was reused; the
	// business function did not run.
	StatusReplayed
	// StatusDuplicate means a tombstone suppressed a duplicate
	// delivery; the business function did not run and the result is
	// no longer retained.
	StatusDuplicate
)

func (s Status) String() string {
	switch s {
	case StatusExecuted:
		return "executed"
	case StatusReplayed:
		return "replayed"
	case StatusDuplicate:
		return "duplicate"
	default:
		return "unknown"
	}
}

// ErrDuplicate is returned by Do when a tombstone suppresses a
// duplicate delivery whose result has already been evicted.
var ErrDuplicate = errors.New("dedup: duplicate delivery, result already evicted")

// entry is a stored result entity.
type entry struct {
	value     interface{}
	pos       Position
	createdAt time.Time
	expiresAt time.Time
}

// Option configures a Store.
type Option func(*Store)

// WithClock overrides the time source, mainly for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// Store is a deduplicating result store. It is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	retention time.Duration
	now       func() time.Time

	entries    map[string]*entry
	tombstones map[string]tombstone
	ledger     *ledger
}

// New creates a Store whose results are retained for at least the
// given retention period after creation.
func New(retention time.Duration, opts ...Option) *Store {
	s := &Store{
		retention:  retention,
		now:        time.Now,
		entries:    make(map[string]*entry),
		tombstones: make(map[string]tombstone),
		ledger:     newLedger(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Do returns the stored result for key, running fn only if the key has
// never been seen. fn is executed at most once per key for as long as
// either the result entity or its tombstone survives.
//
//   - first call: fn runs, result is stored -> (value, StatusExecuted, nil)
//   - result alive: stored value returned -> (value, StatusReplayed, nil)
//   - result evicted, tombstone alive: fn does NOT run ->
//     (nil, StatusDuplicate, ErrDuplicate)
//   - tombstone dropped (watermark confirmed): the source guarantees no
//     redelivery, so the call is treated as new.
func (s *Store) Do(key string, pos Position, fn func() (interface{}, error)) (interface{}, Status, error) {
	s.mu.Lock()
	if e, ok := s.entries[key]; ok {
		v := e.value
		s.mu.Unlock()
		return v, StatusReplayed, nil
	}
	if _, ok := s.tombstones[key]; ok {
		s.mu.Unlock()
		return nil, StatusDuplicate, ErrDuplicate
	}
	s.mu.Unlock()

	v, err := fn()
	if err != nil {
		return nil, StatusExecuted, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check: another goroutine may have completed the same key
	// while fn was running.
	if e, ok := s.entries[key]; ok {
		return e.value, StatusReplayed, nil
	}
	now := s.now()
	s.entries[key] = &entry{
		value:     v,
		pos:       pos,
		createdAt: now,
		expiresAt: now.Add(s.retention),
	}
	return v, StatusExecuted, nil
}

// Acquire takes a reference to the stored result for key. While the
// returned Ref is held, the result cannot be evicted even if its
// retention period has expired. The caller must call Ref.Release when
// done. Returns false if no live result exists for key.
func (s *Store) Acquire(key string) (*Ref, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[key]; !ok {
		return nil, false
	}
	s.ledger.acquire(key)
	return &Ref{store: s, key: key}, true
}

// Evict removes result entities whose retention period has expired AND
// that have no active references in the ledger. Each evicted entity
// leaves behind a tombstone recording its source position, so cleanup
// can never cause the business function to re-execute for a duplicate
// delivery. It returns the keys that were evicted.
func (s *Store) Evict() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var evicted []string
	for key, e := range s.entries {
		if now.Before(e.expiresAt) {
			continue // retention period not over
		}
		if s.ledger.count(key) > 0 {
			continue // active references pin the result
		}
		s.tombstones[key] = tombstone{pos: e.pos, createdAt: now}
		delete(s.entries, key)
		evicted = append(evicted, key)
	}
	return evicted
}

// HasResult reports whether a live result entity exists for key.
func (s *Store) HasResult(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[key]
	return ok
}

// HasTombstone reports whether a tombstone exists for key.
func (s *Store) HasTombstone(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tombstones[key]
	return ok
}

// Verify checks the reference ledger against the store's contents:
// reference counts must be non-negative and must never point at a key
// whose result entity is gone. It returns an error describing the
// first violated invariant.
func (s *Store) Verify() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, n := range s.ledger.snapshot() {
		if n < 0 {
			return fmt.Errorf("dedup: negative reference count %d for key %q", n, key)
		}
		if n > 0 {
			if _, ok := s.entries[key]; !ok {
				return fmt.Errorf("dedup: %d dangling reference(s) to evicted key %q", n, key)
			}
		}
	}
	return nil
}
