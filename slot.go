package netsec

import (
	"sync"
	"sync/atomic"
)

// Slot is one lazy binding jump slot. The hot path (state observation and
// publication) uses atomic operations; a mutex coordinates state
// transitions and the per-attempt completion channel.
type Slot struct {
	id SlotID

	mu sync.Mutex

	// state is read atomically on the fast path and mutated under mu.
	state atomic.Uint32

	// gen is bumped on every invalidation so callbacks from an older
	// generation can never publish into the current slot.
	gen uint64

	// target is the published jump target; only meaningful in StateBound.
	// It is written before the atomic state flip to StateBound.
	target uint64

	// moduleID records the module that backs the published target, so a
	// module unload can invalidate exactly the slots that point into it.
	moduleID string

	// lastErr is the error recorded by the most recent failed attempt.
	lastErr error

	// done is closed when the current resolving attempt finishes; it is
	// nil when no attempt is in flight.
	done chan struct{}
}

// SlotInfo is an immutable diagnostic snapshot of a slot.
type SlotInfo struct {
	ID         SlotID
	State      SlotState
	Generation uint64
	Target     uint64
	ModuleID   string
	Err        error
}

func newSlot(id SlotID) *Slot {
	s := &Slot{id: id}
	s.state.Store(uint32(StateUnresolved))
	return s
}

// cachedSymbol returns the bound symbol without entering the resolution
// path. The atomic state load is the lock-free fast path; the lock is only
// taken once a Bound state has been observed, to read target and moduleID
// with the happens-before edge established by publish.
func (s *Slot) cachedSymbol(id SlotID) (Symbol, bool) {
	if SlotState(s.state.Load()) != StateBound {
		return Symbol{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if SlotState(s.state.Load()) != StateBound {
		return Symbol{}, false
	}
	return Symbol{Slot: id, Module: s.moduleID, Address: s.target}, true
}

// begin starts or joins a resolution attempt. When acquired is true the
// caller owns a new attempt for the returned generation and must finish it
// via publish/fail. Otherwise state reports the slot's current state
// (StateResolving with a channel to wait on, or StateBound to re-read the
// cached target).
func (s *Slot) begin() (gen uint64, done chan struct{}, state SlotState, acquired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := SlotState(s.state.Load())
	if current == StateUnresolved || current == StateFailed {
		s.state.Store(uint32(StateResolving))
		s.lastErr = nil
		if s.done == nil {
			s.done = make(chan struct{})
		}
		return s.gen, s.done, StateResolving, true
	}
	return s.gen, s.done, current, false
}

// publish atomically exposes a verified target. It succeeds only when the
// slot is still Resolving for exactly the generation that owns the
// callback; otherwise the result is rejected (stale callback) and the
// current generation is left untouched.
func (s *Slot) publish(gen uint64, target uint64, moduleID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gen != gen || SlotState(s.state.Load()) != StateResolving {
		return false
	}
	s.target = target
	s.moduleID = moduleID
	s.state.Store(uint32(StateBound))
	close(s.done)
	s.done = nil
	return true
}

// fail records a failed attempt. Like publish it is generation scoped: a
// stale failure never overwrites a newer attempt.
func (s *Slot) fail(gen uint64, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gen != gen || SlotState(s.state.Load()) != StateResolving {
		return false
	}
	s.lastErr = err
	s.state.Store(uint32(StateFailed))
	close(s.done)
	s.done = nil
	return true
}

// invalidateForModule rolls a slot back to Unresolved under a new
// generation when a module unload may affect it:
//
//   - a Resolving slot is always invalidated, because the module its
//     callback will return cannot be known until publication;
//   - a Bound slot is invalidated only when it points into the unloaded
//     module;
//   - Failed/Unresolved slots have no in-flight callback and keep their
//     generation.
//
// Waiters blocked on the old attempt are released by closing its channel.
func (s *Slot) invalidateForModule(moduleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := SlotState(s.state.Load())
	if state != StateResolving &&
		!(state == StateBound && s.moduleID == moduleID) {
		return
	}

	s.gen++
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	s.target = 0
	s.moduleID = ""
	s.lastErr = nil
	s.state.Store(uint32(StateUnresolved))
}

// invalidate unconditionally rolls a slot to a fresh generation, used by
// the explicit Binder.Invalidate entry point.
func (s *Slot) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gen++
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	s.target = 0
	s.moduleID = ""
	s.lastErr = nil
	s.state.Store(uint32(StateUnresolved))
}

// snapshot copies the observable slot state under the lock.
func (s *Slot) snapshot() SlotInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SlotInfo{
		ID:         s.id,
		State:      SlotState(s.state.Load()),
		Generation: s.gen,
		Target:     s.target,
		ModuleID:   s.moduleID,
		Err:        s.lastErr,
	}
}
