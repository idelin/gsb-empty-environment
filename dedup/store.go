// Package dedup implements a deduplication result store.
//
// Results recorded for a dedup key may be referenced by subsequent
// operations. A result entity is evicted only when its retention period
// has expired AND it has no active references. Anti-duplicate tombstones
// have an independent lifecycle: a tombstone outlives its result and is
// removed only after the source watermark has been confirmed past the
// position the record was ingested at.
//
// A reference ledger (Ledger) accounts for every acquire/release and
// every eviction, and can verify that cleanup never strands a live
// reference — i.e. that garbage collection can never make a known key
// become unknown, which would force the business operation to re-execute.
package dedup

import (
	"errors"
	"sync"
	"time"
)

// State is the resolution outcome for a dedup key.
type State int

const (
	// StateUnknown means neither a result nor a tombstone exists; the
	// caller must execute the business operation.
	StateUnknown State = iota
	// StateLive means a result entity is present; reuse it.
	StateLive
	// StateDuplicate means the result was collected but a tombstone
	// confirms the key was already processed; do not re-execute.
	StateDuplicate
)

func (s State) String() string {
	switch s {
	case StateLive:
		return "live"
	case StateDuplicate:
		return "duplicate"
	default:
		return "unknown"
	}
}

// ErrKeyUnknown is returned by Acquire when no result entity exists.
var ErrKeyUnknown = errors.New("dedup: no live result for key")

// Result is a stored outcome of a processed operation.
type Result struct {
	Key       string
	Value     interface{}
	SourceSeq uint64
	CreatedAt time.Time
	ExpiresAt time.Time
	refs      int
}

// Refs reports the number of active references to the result.
func (r *Result) Refs() int { return r.refs }

// Tombstone is an anti-duplicate marker. It survives its result entity
// and is removed only once the source watermark passes SourceSeq.
type Tombstone struct {
	Key       string
	SourceSeq uint64
	CreatedAt time.Time
}

// Handle is a checked-out reference to a result. Release it exactly once.
type Handle struct {
	store *Store
	key   string
	done  bool
}

// Value returns the referenced result value.
func (h *Handle) Value() interface{} {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if r, ok := h.store.results[h.key]; ok {
		return r.Value
	}
	return nil
}

// Store is a dedup result store with a reference ledger.
type Store struct {
	mu         sync.Mutex
	retention  time.Duration
	results    map[string]*Result
	tombstones map[string]*Tombstone
	watermark  uint64
	ledger     *Ledger
	now        func() time.Time
}

// NewStore creates a store whose results live for retention after their
// last record, and may live longer while referenced.
func NewStore(retention time.Duration) *Store {
	return &Store{
		retention:  retention,
		results:    make(map[string]*Result),
		tombstones: make(map[string]*Tombstone),
		ledger:     NewLedger(),
		now:        time.Now,
	}
}

// SetClock overrides the time source (for tests).
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Ledger exposes the store's reference ledger for verification.
func (s *Store) Ledger() *Ledger { return s.ledger }

// Record stores the outcome of an executed operation for key, refreshing
// retention, and ensures a tombstone exists at the given source position.
func (s *Store) Record(key string, value interface{}, sourceSeq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if r, ok := s.results[key]; ok {
		r.Value = value
		r.ExpiresAt = now.Add(s.retention)
	} else {
		s.results[key] = &Result{
			Key:       key,
			Value:     value,
			SourceSeq: sourceSeq,
			CreatedAt: now,
			ExpiresAt: now.Add(s.retention),
		}
	}
	if _, ok := s.tombstones[key]; !ok {
		s.tombstones[key] = &Tombstone{Key: key, SourceSeq: sourceSeq, CreatedAt: now}
	}
}

// Resolve reports whether key is known without taking a reference.
// StateUnknown means the business operation must be executed.
func (s *Store) Resolve(key string) (State, interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.results[key]; ok {
		return StateLive, r.Value
	}
	if _, ok := s.tombstones[key]; ok {
		return StateDuplicate, nil
	}
	return StateUnknown, nil
}

// Acquire takes a reference on the live result for key. While a reference
// is held, Sweep will not evict the result even past its retention.
func (s *Store) Acquire(key string) (*Handle, error) {
	s.mu.Lock()
	r, ok := s.results[key]
	if !ok {
		s.mu.Unlock()
		return nil, ErrKeyUnknown
	}
	r.refs++
	s.ledger.acquire(key)
	s.mu.Unlock()
	return &Handle{store: s, key: key}, nil
}

// Release returns the reference. It is idempotent.
func (h *Handle) Release() {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if h.done {
		return
	}
	h.done = true
	if r, ok := h.store.results[h.key]; ok && r.refs > 0 {
		r.refs--
	}
	h.store.ledger.release(h.key)
}

// AdvanceWatermark confirms the source has durably consumed up to seq.
// Tombstones at or below seq become eligible for collection.
func (s *Store) AdvanceWatermark(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.watermark {
		s.watermark = seq
	}
}

// Sweep collects expired, unreferenced results and watermark-confirmed
// tombstones. Every eviction is written to the ledger.
func (s *Store) Sweep() (evictedResults, evictedTombstones int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, r := range s.results {
		if r.refs == 0 && !now.Before(r.ExpiresAt) {
			delete(s.results, key)
			s.ledger.evictResult(key, r)
			evictedResults++
		}
	}
	for key, t := range s.tombstones {
		if t.SourceSeq <= s.watermark {
			delete(s.tombstones, key)
			s.ledger.evictTombstone(key, t)
			evictedTombstones++
		}
	}
	return evictedResults, evictedTombstones
}

// Stats returns counts of live results and tombstones.
func (s *Store) Stats() (results, tombstones int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.results), len(s.tombstones)
}
