// Package arbiter implements a multi-node contention and conflict arbiter.
//
// Nodes compete to write versioned slots. Every write is a compare-and-swap
// on the slot's version, so concurrent proposals are linearized: for each
// key, accepted versions form the gap-free sequence 1, 2, 3, ... and each
// version has exactly one winning writer.
//
// Multi-key transactions are atomic (all-or-nothing) and deadlock-free:
// slots are always locked in a globally sorted key order, which breaks the
// circular-wait condition. The arbiter itself never spawns goroutines.
package arbiter

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrConflict is returned (wrapped) when an expected version does not match
// the slot's current version, i.e. the proposal lost the race.
var ErrConflict = fmt.Errorf("arbiter: version conflict")

// ConflictError identifies which key rejected a proposal and why.
type ConflictError struct {
	Key     string
	Expect  uint64
	Current uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%v: key %q: expected version %d, current is %d",
		ErrConflict, e.Key, e.Expect, e.Current)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// Op is one compare-and-swap inside a transaction: write Value to Key only
// if the slot's current version equals Expect.
type Op struct {
	Key    string
	Expect uint64
	Value  any
}

type slot struct {
	mu      sync.Mutex
	version uint64
	value   any
	writer  int // node ID of the last accepted writer; -1 when untouched
}

// Arbiter coordinates conflict-free linearizable access to versioned slots.
type Arbiter struct {
	mu    sync.Mutex // guards slots map only; never held while locking a slot
	slots map[string]*slot

	accepted atomic.Uint64
	rejected atomic.Uint64
}

// New returns an empty Arbiter.
func New() *Arbiter {
	return &Arbiter{slots: make(map[string]*slot)}
}

// slotFor returns the slot for key, creating it on first use.
func (a *Arbiter) slotFor(key string) *slot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.slots[key]
	if !ok {
		s = &slot{writer: -1}
		a.slots[key] = s
	}
	return s
}

// Propose attempts a single-key compare-and-swap for nodeID. On success it
// returns the new version (Expect+1) and true; on conflict it returns the
// current version and false. The operation is linearizable: it takes effect
// at the instant the slot lock is held.
func (a *Arbiter) Propose(key string, nodeID int, expect uint64, value any) (uint64, bool) {
	s := a.slotFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.version != expect {
		a.rejected.Add(1)
		return s.version, false
	}
	s.version++
	s.value = value
	s.writer = nodeID
	a.accepted.Add(1)
	return s.version, true
}

// Read returns a linearizable snapshot of a slot.
func (a *Arbiter) Read(key string) (version uint64, value any, ok bool) {
	a.mu.Lock()
	s, exists := a.slots[key]
	a.mu.Unlock()
	if !exists {
		return 0, nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version, s.value, true
}

// Transact atomically applies ops for nodeID: either every compare-and-swap
// succeeds or none does. Locks are acquired in sorted key order, so
// concurrent transactions cannot deadlock regardless of operand order.
func (a *Arbiter) Transact(nodeID int, ops ...Op) error {
	if len(ops) == 0 {
		return nil
	}
	// Deterministic global lock ordering => no circular wait => no deadlock.
	sorted := make([]Op, len(ops))
	copy(sorted, ops)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Key == sorted[i-1].Key {
			return fmt.Errorf("arbiter: duplicate key %q in transaction", sorted[i].Key)
		}
	}

	locked := make([]*slot, len(sorted))
	for i, op := range sorted {
		s := a.slotFor(op.Key)
		s.mu.Lock()
		locked[i] = s
	}
	defer func() {
		for _, s := range locked {
			s.mu.Unlock()
		}
	}()

	// Validation phase: every expectation must hold before any write.
	for i, op := range sorted {
		if locked[i].version != op.Expect {
			a.rejected.Add(1)
			return &ConflictError{Key: op.Key, Expect: op.Expect, Current: locked[i].version}
		}
	}
	// Commit phase: cannot fail.
	for i, op := range sorted {
		locked[i].version++
		locked[i].value = op.Value
		locked[i].writer = nodeID
	}
	a.accepted.Add(uint64(len(sorted)))
	return nil
}

// Stats returns the number of accepted and rejected write operations.
func (a *Arbiter) Stats() (accepted, rejected uint64) {
	return a.accepted.Load(), a.rejected.Load()
}
