package writeoff

import (
	"context"
	"sync"
)

// Store persists payment aggregates. All write-off state changes happen inside
// Mutate so that load, validation and state transition are one atomic unit.
//
// Database implementations must execute mutate inside a single transaction:
// load the aggregate with a row lock, apply the transition, commit on nil
// error and roll back otherwise. Returning an error from mutate must leave the
// stored state unchanged.
type Store interface {
	// Load returns a copy of the aggregate for txnID, or ErrPaymentNotFound.
	Load(ctx context.Context, txnID string) (Aggregate, error)

	// Mutate atomically loads the aggregate for txnID (a zero-value Aggregate
	// when it does not yet exist), invokes mutate and persists the returned
	// aggregate only when mutate returns nil.
	Mutate(ctx context.Context, txnID string, mutate func(Aggregate) (Aggregate, error)) (Aggregate, error)

	// List returns copies of all stored aggregates.
	List(ctx context.Context) ([]Aggregate, error)
}

// MemoryStore is a goroutine-safe in-process Store. A single mutex serializes
// every read-modify-write cycle, which makes concurrent replayed or duplicate
// confirmations safe within one process.
type MemoryStore struct {
	mu    sync.Mutex
	items map[string]Aggregate
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]Aggregate)}
}

// Load implements Store.
func (s *MemoryStore) Load(_ context.Context, txnID string) (Aggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agg, ok := s.items[txnID]
	if !ok {
		return Aggregate{}, ErrPaymentNotFound
	}
	return cloneAggregate(agg), nil
}

// Mutate implements Store.
func (s *MemoryStore) Mutate(_ context.Context, txnID string, mutate func(Aggregate) (Aggregate, error)) (Aggregate, error) {
	if mutate == nil {
		return Aggregate{}, ErrInvalidState
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.items[txnID]
	if !ok {
		current = Aggregate{}
	}

	next, err := mutate(cloneAggregate(current))
	if err != nil {
		return Aggregate{}, err
	}
	s.items[txnID] = cloneAggregate(next)
	return cloneAggregate(next), nil
}

// List implements Store.
func (s *MemoryStore) List(_ context.Context) ([]Aggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Aggregate, 0, len(s.items))
	for _, agg := range s.items {
		out = append(out, cloneAggregate(agg))
	}
	return out, nil
}

// cloneAggregate returns a deep copy so callers cannot mutate stored pointers.
func cloneAggregate(in Aggregate) Aggregate {
	out := in
	if in.Confirm != nil {
		confirm := *in.Confirm
		out.Confirm = &confirm
	}
	if in.Reversal != nil {
		reversal := *in.Reversal
		out.Reversal = &reversal
	}
	return out
}
