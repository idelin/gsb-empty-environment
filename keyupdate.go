package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// DefaultMaxConsecutiveUpdates is the default number of KeyUpdate messages
// that may occur in one uninterrupted run of key-update activity.
//
// RFC 8446 permits implementations to bound the number of KeyUpdate messages
// they process. Keeping a default is important because a peer can otherwise
// force the receiver to retain an unbounded number of traffic-key epochs.
const DefaultMaxConsecutiveUpdates = 32

// Unlimited disables the consecutive-update limit. It should only be used when
// another layer enforces an equivalent bound.
const Unlimited = -1

var (
	// ErrTooManyKeyUpdates is returned when a connection exceeds the
	// caller-configured number of consecutive key updates.
	ErrTooManyKeyUpdates = errors.New("netsec: too many consecutive key updates")

	// ErrKeyUpdatePending is returned when a new update is attempted while
	// the previous update has not been acknowledged.
	ErrKeyUpdatePending = errors.New("netsec: previous key update is unacknowledged")

	// ErrUnknownEvent is returned by Apply for an unsupported event.
	ErrUnknownEvent = errors.New("netsec: unknown key update event")
)

// ErrTooManyUpdates is an alias for ErrTooManyKeyUpdates.
var ErrTooManyUpdates = ErrTooManyKeyUpdates

// Event is a state transition reported by the TLS stack.
type Event uint8

const (
	// EventKeyUpdate reports that a KeyUpdate was sent or received. Each
	// accepted event advances the traffic-key generation exactly once.
	EventKeyUpdate Event = iota + 1

	// EventAcknowledged reports that the peer confirmed the outstanding
	// KeyUpdate. It makes the state machine ready for another update but
	// deliberately does not reset the consecutive-update budget.
	EventAcknowledged

	// EventProgress reports non-KeyUpdate traffic (normally application
	// data). It confirms any outstanding update and starts a new run of
	// consecutive key-update activity.
	EventProgress

	// EventReset starts a fresh connection state.
	EventReset
)

// Snapshot is a consistent view of a StateMachine.
type Snapshot struct {
	// Generation is the number of accepted key updates.
	Generation uint64
	// Pending reports whether the latest accepted update is unacknowledged.
	Pending bool
	// PendingGeneration is the generation of the pending update, or zero if
	// no update is pending.
	PendingGeneration uint64
	// Consecutive is the number of updates in the current run.
	Consecutive int
	// Limit is the configured limit, or zero when unlimited.
	Limit int
	// Err is the terminal error, if the machine has failed.
	Err error
}

// StateMachine tracks TLS 1.3 KeyUpdate state without performing any I/O.
//
// It is intended to be embedded in, or called from, a TLS stack. The stack
// reports key-update events and the state machine enforces two independent
// invariants:
//
//   - At most one update may be outstanding at a time. A second update is
//     rejected until Acknowledge is called.
//   - A run of key-update activity may not exceed the caller-supplied limit.
//     The run is reset by non-KeyUpdate traffic, not by acknowledgements, so a
//     peer cannot avoid the limit by acknowledging every update.
//
// Once the limit is exceeded, the failure is terminal until Reset is called.
// This mirrors the connection-fatal handling required for a protocol
// violation. Methods are safe for concurrent use.
type StateMachine struct {
	mu sync.Mutex

	limit       int
	unlimited   bool
	generation  uint64
	consecutive int
	pending     bool
	pendingGen  uint64
	err         error
}

// KeyUpdateState is an alias for StateMachine.
type KeyUpdateState = StateMachine

// KeyUpdateTracker is an alias for StateMachine.
type KeyUpdateTracker = StateMachine

// Tracker is an alias for StateMachine.
type Tracker = StateMachine

// NewStateMachine returns a state machine with the given consecutive-update
// limit. A zero limit selects DefaultMaxConsecutiveUpdates. A negative limit,
// including Unlimited, disables the limit.
func NewStateMachine(maxConsecutiveUpdates int) *StateMachine {
	switch {
	case maxConsecutiveUpdates == 0:
		maxConsecutiveUpdates = DefaultMaxConsecutiveUpdates
	case maxConsecutiveUpdates < 0:
		return &StateMachine{unlimited: true}
	}
	return &StateMachine{limit: maxConsecutiveUpdates}
}

// New returns a state machine with the given consecutive-update limit.
func New(maxConsecutiveUpdates int) *StateMachine {
	return NewStateMachine(maxConsecutiveUpdates)
}

// NewKeyUpdateState returns a state machine with the given limit.
func NewKeyUpdateState(maxConsecutiveUpdates int) *StateMachine {
	return NewStateMachine(maxConsecutiveUpdates)
}

// NewTracker returns a state machine with the given limit.
func NewTracker(maxConsecutiveUpdates int) *StateMachine {
	return NewStateMachine(maxConsecutiveUpdates)
}

// NewDefault returns a state machine using DefaultMaxConsecutiveUpdates.
func NewDefault() *StateMachine {
	return NewStateMachine(DefaultMaxConsecutiveUpdates)
}

// Update records a KeyUpdate and advances the traffic-key generation.
//
// It fails if an earlier update is still unacknowledged or if the
// consecutive-update limit has been reached. A limit violation is terminal.
func (s *StateMachine) Update() error {
	return s.Apply(EventKeyUpdate)
}

// SendKeyUpdate records a locally sent KeyUpdate.
func (s *StateMachine) SendKeyUpdate() error {
	return s.Update()
}

// ReceiveKeyUpdate records a KeyUpdate received from the peer.
func (s *StateMachine) ReceiveKeyUpdate() error {
	return s.Update()
}

// KeyUpdate records a KeyUpdate event. It is equivalent to Update.
func (s *StateMachine) KeyUpdate() error {
	return s.Update()
}

// Acknowledge records the peer's confirmation of the outstanding update.
//
// Duplicate acknowledgements are harmless. Acknowledgement alone does not
// reset the consecutive-update budget; ApplicationData or Progress does that.
func (s *StateMachine) Acknowledge() error {
	return s.Apply(EventAcknowledged)
}

// Ack is an alias for Acknowledge.
func (s *StateMachine) Ack() error {
	return s.Acknowledge()
}

// Confirm is an alias for Acknowledge.
func (s *StateMachine) Confirm() error {
	return s.Acknowledge()
}

// ApplicationData records non-KeyUpdate traffic. It confirms any outstanding
// update and resets the consecutive-update run.
func (s *StateMachine) ApplicationData() error {
	return s.Apply(EventProgress)
}

// Progress is an alias for ApplicationData.
func (s *StateMachine) Progress() error {
	return s.ApplicationData()
}

// Apply performs a state transition reported by the TLS stack.
func (s *StateMachine) Apply(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch event {
	case EventKeyUpdate:
		return s.updateLocked()
	case EventAcknowledged:
		if s.err != nil {
			return s.err
		}
		s.pending = false
		s.pendingGen = 0
		return nil
	case EventProgress:
		if s.err != nil {
			return s.err
		}
		s.pending = false
		s.pendingGen = 0
		s.consecutive = 0
		return nil
	case EventReset:
		s.resetLocked()
		return nil
	default:
		return fmt.Errorf("%w: %d", ErrUnknownEvent, event)
	}
}

func (s *StateMachine) updateLocked() error {
	if s.err != nil {
		return s.err
	}
	if s.pending {
		return ErrKeyUpdatePending
	}
	limit := s.effectiveLimitLocked()
	if limit > 0 && s.consecutive >= limit {
		s.err = ErrTooManyKeyUpdates
		return s.err
	}

	s.generation++
	s.consecutive++
	s.pending = true
	s.pendingGen = s.generation
	return nil
}

// Reset returns the machine to its initial state, including clearing a
// terminal limit error. It should be called when a new TLS connection is
// established.
func (s *StateMachine) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

func (s *StateMachine) resetLocked() {
	s.generation = 0
	s.consecutive = 0
	s.pending = false
	s.pendingGen = 0
	s.err = nil
}

// Pending reports whether an update is awaiting acknowledgement.
func (s *StateMachine) Pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

// Consecutive returns the number of updates in the current run.
func (s *StateMachine) Consecutive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consecutive
}

// Generation returns the current traffic-key generation.
func (s *StateMachine) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// Epoch is an alias for Generation.
func (s *StateMachine) Epoch() uint64 {
	return s.Generation()
}

// Limit returns the configured consecutive-update limit. Zero means that no
// limit is configured.
func (s *StateMachine) Limit() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.effectiveLimitLocked()
}

func (s *StateMachine) effectiveLimitLocked() int {
	if s.unlimited {
		return 0
	}
	if s.limit == 0 {
		return DefaultMaxConsecutiveUpdates
	}
	return s.limit
}

// Err returns the terminal error, if any.
func (s *StateMachine) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Failed reports whether the machine has entered a terminal error state.
func (s *StateMachine) Failed() bool {
	return s.Err() != nil
}

// Snapshot returns a consistent view of the machine.
func (s *StateMachine) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Generation:        s.generation,
		Pending:           s.pending,
		PendingGeneration: s.pendingGen,
		Consecutive:       s.consecutive,
		Limit:             s.effectiveLimitLocked(),
		Err:               s.err,
	}
}
