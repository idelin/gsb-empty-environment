package session

import (
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by engine operations. Callers use errors.Is to
// distinguish them.
var (
	// ErrInvalidTuple is returned for an unknown direction or an invalid flow.
	ErrInvalidTuple = errors.New("session: invalid flow or direction")
	// ErrConnectionDenied is returned when the policy rejects a new connection.
	ErrConnectionDenied = errors.New("session: connection denied by policy")
	// ErrSessionUnknown is returned when no active session matches the tuple.
	ErrSessionUnknown = errors.New("session: unknown session")
	// ErrSessionRevoked is returned for a session revoked by a policy update.
	ErrSessionRevoked = errors.New("session: session revoked by policy update")
	// ErrSessionClosed is returned for a session that already ended.
	ErrSessionClosed = errors.New("session: session already closed")
	// ErrEventOrder is returned by VerifyOrder when the event log is not linear.
	ErrEventOrder = errors.New("session: event log is not linearly ordered")
)

// Session is an immutable snapshot of a tracked session.
type Session struct {
	Tuple     Tuple
	State     State
	StartedAt time.Time
	UpdatedAt time.Time
}

// activeEntry is the mutable internal record behind a Session snapshot.
type activeEntry struct {
	state     State
	startedAt time.Time
	updatedAt time.Time
}

// UpdateResult summarizes one policy update.
type UpdateResult struct {
	// Generation is the new current generation after the update.
	Generation uint64
	// Revoked lists tuples that were active and have been revoked.
	Revoked []Tuple
	// Missing lists requested tuples that had no active session.
	Missing []Tuple
}

// Engine is the generation-aware session decision engine. Its zero value is
// not usable; construct one with NewEngine.
type Engine struct {
	mu        sync.Mutex
	policy    Policy
	clock     Clock
	scheduler Scheduler

	generation uint64
	sessions   map[Tuple]*activeEntry
	log        []Event
	seq        int64
}

// NewEngine builds an engine at generation 1. A nil clock or scheduler is
// replaced with SystemClock and a fresh SerialScheduler respectively.
func NewEngine(policy Policy, clock Clock, scheduler Scheduler) *Engine {
	if policy == nil {
		panic("session: NewEngine requires a Policy")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	if scheduler == nil {
		scheduler = NewSerialScheduler()
	}
	return &Engine{
		policy:     policy,
		clock:      clock,
		scheduler:  scheduler,
		generation: 1,
		sessions:   make(map[Tuple]*activeEntry),
	}
}

// Generation returns the current policy generation.
func (e *Engine) Generation() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generation
}

// Connect establishes a session for a flow/direction pair. The resulting
// tuple pins the current generation, so later policy updates cannot change
// the authorization of this connection. Reusing the same transport 5-tuple
// after the previous session ended always produces a fresh tuple evaluated
// against the current policy and never inherits an older authorization.
func (e *Engine) Connect(flow FlowID, dir Direction) (Tuple, error) {
	if !flow.Valid() || dir == DirectionUnknown {
		return Tuple{}, ErrInvalidTuple
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	tuple := Tuple{
		Flow:       flow,
		Direction:  dir,
		StartupGen: e.generation,
	}

	if entry, ok := e.sessions[tuple]; ok && entry.state.IsActive() {
		return tuple, nil
	}

	decision := e.policy.Decide(flow, dir)
	e.recordLocked(EventConnect, tuple, decision)
	if decision == DecisionDeny {
		return Tuple{}, ErrConnectionDenied
	}

	now := e.clock.Now()
	e.sessions[tuple] = &activeEntry{
		state:     StateNew,
		startedAt: now,
		updatedAt: now,
	}
	return tuple, nil
}

// Authorize checks whether an already established, generation-pinned session
// may carry traffic. The decision is made from the session state, not from
// the current policy.
func (e *Engine) Authorize(tuple Tuple) (Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	entry, ok := e.sessions[tuple]
	if !ok {
		return DecisionDeny, ErrSessionUnknown
	}
	switch entry.state {
	case StateRevoked:
		return DecisionDeny, ErrSessionRevoked
	case StateClosed:
		return DecisionDeny, ErrSessionClosed
	default:
		return DecisionAllow, nil
	}
}

// SendFirstPacket authorizes and records the first packet of a session. The
// session moves from StateNew to StateEstablished. Reusing the transport
// tuple of an ended session fails because its generation-pinned tuple is no
// longer active.
func (e *Engine) SendFirstPacket(tuple Tuple) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	entry, ok := e.sessions[tuple]
	if !ok {
		return ErrSessionUnknown
	}
	if entry.state == StateRevoked {
		return ErrSessionRevoked
	}
	if entry.state == StateClosed {
		return ErrSessionClosed
	}

	e.recordLocked(EventFirstPacket, tuple, DecisionAllow)
	if entry.state == StateNew {
		entry.state = StateEstablished
		entry.updatedAt = e.clock.Now()
	}
	return nil
}

// End closes the exact session identified by tuple. An end event carrying an
// old generation never removes a newer session because tuple keys include the
// startup generation.
func (e *Engine) End(tuple Tuple) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	entry, ok := e.sessions[tuple]
	if !ok {
		return ErrSessionUnknown
	}
	if entry.state == StateRevoked {
		return ErrSessionRevoked
	}
	if entry.state == StateClosed {
		return ErrSessionClosed
	}

	entry.state = StateClosed
	entry.updatedAt = e.clock.Now()
	e.recordLocked(EventEnd, tuple, DecisionAllow)
	return nil
}

// Update advances the policy generation. The new policy applies only to
// connections established after the update unless WithRevocation is supplied:
// the listed active sessions are then revoked as part of the same update.
func (e *Engine) Update(policy Policy, opts ...UpdateOption) (UpdateResult, error) {
	if policy == nil {
		return UpdateResult{}, errors.New("session: Update requires a Policy")
	}

	options := updateOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.policy = policy
	e.generation++
	result := UpdateResult{Generation: e.generation}
	e.recordLocked(EventPolicyUpdate, Tuple{}, DecisionDeny)

	for _, tuple := range options.revoke {
		entry, ok := e.sessions[tuple]
		if !ok || !entry.state.IsActive() {
			result.Missing = append(result.Missing, tuple)
			continue
		}
		entry.state = StateRevoked
		entry.updatedAt = e.clock.Now()
		result.Revoked = append(result.Revoked, tuple)
		e.recordLocked(EventRevoke, tuple, DecisionDeny)
	}
	return result, nil
}

// Lookup returns a snapshot of the session for tuple, or false when absent.
// Closed and revoked sessions remain visible so callers can distinguish a
// stale tuple from an unknown one.
func (e *Engine) Lookup(tuple Tuple) (Session, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	entry, ok := e.sessions[tuple]
	if !ok {
		return Session{}, false
	}
	return e.snapshotLocked(tuple, entry), true
}

// Sessions returns a snapshot of every tracked session, including closed and
// revoked ones, keyed by generation-pinned tuple.
func (e *Engine) Sessions() map[Tuple]Session {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make(map[Tuple]Session, len(e.sessions))
	for tuple, entry := range e.sessions {
		out[tuple] = e.snapshotLocked(tuple, entry)
	}
	return out
}

// Events returns a copy of the linear event log.
func (e *Engine) Events() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Event, len(e.log))
	copy(out, e.log)
	return out
}

// VerifyOrder validates the engine invariants on the event log: dispatch
// sequence numbers and timestamps must be strictly increasing, policy
// updates must be chronologically ordered against connection/first-packet
// events, and every session event must refer to a session whose startup
// generation does not exceed the generation in effect at that time.
func (e *Engine) VerifyOrder() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var prevSeq int64
	var prevAt time.Time
	var currentGen uint64 = 1

	for _, event := range e.log {
		if event.Seq <= prevSeq {
			return ErrEventOrder
		}
		if !prevAt.IsZero() && event.At.Before(prevAt) {
			return ErrEventOrder
		}

		switch event.Kind {
		case EventPolicyUpdate:
			currentGen = event.Gen
		case EventConnect:
			if event.Tuple.StartupGen != currentGen {
				return ErrEventOrder
			}
		case EventFirstPacket, EventEnd, EventRevoke:
			if event.Gen < event.Tuple.StartupGen {
				return ErrEventOrder
			}
		default:
			return ErrEventOrder
		}

		prevSeq = event.Seq
		prevAt = event.At
	}
	return nil
}

// ScheduledConnect enqueues a Connect onto the injected scheduler.
func (e *Engine) ScheduledConnect(flow FlowID, dir Direction) {
	e.scheduler.Schedule(func() { _, _ = e.Connect(flow, dir) })
}

// ScheduledFirstPacket enqueues a SendFirstPacket onto the scheduler.
func (e *Engine) ScheduledFirstPacket(tuple Tuple) {
	e.scheduler.Schedule(func() { _ = e.SendFirstPacket(tuple) })
}

// ScheduledEnd enqueues an End onto the scheduler.
func (e *Engine) ScheduledEnd(tuple Tuple) {
	e.scheduler.Schedule(func() { _ = e.End(tuple) })
}

// ScheduledUpdate enqueues an Update onto the scheduler.
func (e *Engine) ScheduledUpdate(policy Policy, opts ...UpdateOption) {
	e.scheduler.Schedule(func() { _, _ = e.Update(policy, opts...) })
}

// Pending returns the number of tasks queued on the injected scheduler.
func (e *Engine) Pending() int { return e.scheduler.Pending() }

// DispatchOne dispatches one scheduled task and reports whether one ran.
func (e *Engine) DispatchOne() bool { return e.scheduler.DispatchOne() }

// DispatchAll dispatches every scheduled task in enqueue order.
func (e *Engine) DispatchAll() { e.scheduler.DispatchAll() }

// recordLocked appends an event. Callers must hold e.mu.
func (e *Engine) recordLocked(kind EventKind, tuple Tuple, decision Decision) {
	e.seq++
	e.log = append(e.log, Event{
		Seq:      e.seq,
		At:       e.clock.Now(),
		Kind:     kind,
		Tuple:    tuple,
		Gen:      e.generation,
		Decision: decision,
	})
}

// snapshotLocked copies an internal entry into an immutable Session.
func (e *Engine) snapshotLocked(tuple Tuple, entry *activeEntry) Session {
	return Session{
		Tuple:     tuple,
		State:     entry.state,
		StartedAt: entry.startedAt,
		UpdatedAt: entry.updatedAt,
	}
}
