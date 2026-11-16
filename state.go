package netsec

import (
	"errors"
	"sync"
	"time"
)

// Sentinel errors are the security-relevant rejection reasons. They are
// returned wrapped inside a Decision whose Kind is Reject; callers can use
// errors.Is to distinguish the exact cause.
var (
	// ErrUnsupportedType is returned for any type other than single command.
	ErrUnsupportedType = errors.New("netsec: unsupported type, only single command (45) is handled")
	// ErrSelectRequired rejects a direct execute (S/E=0) on a point that is
	// configured select-before-execute, or an execute whose selection is
	// missing because it never happened or targeted another IOA.
	ErrSelectRequired = errors.New("netsec: no valid selection for this connection and IOA")
	// ErrConnectionMismatch rejects a frame that tries to use a selection
	// owned by another connection.
	ErrConnectionMismatch = errors.New("netsec: selection is owned by another connection")
	// ErrValueMismatch rejects an execute whose command value differs from
	// the value given at selection time.
	ErrValueMismatch = errors.New("netsec: execute command value differs from selected value")
	// ErrSelectionExpired rejects an execute arriving after the 30 second
	// select window; the stale selection has already been voided.
	ErrSelectionExpired = errors.New("netsec: selection timed out")
	// ErrUnknownPoint rejects traffic for an IOA the slave has no point for,
	// when RejectUnknownPoints is enabled.
	ErrUnknownPoint = errors.New("netsec: unknown information object address")
)

// Kind classifies the outcome of feeding one command frame to the slave.
type Kind int

const (
	// Selected records an accepted select; a pending selection is held.
	Selected Kind = iota
	// Executed records an accepted execute; the command may be applied.
	Executed
	// Cancelled records an accepted cancellation; the selection was cleared.
	Cancelled
	// Reject records a refused frame; Reason carries the exact cause.
	Reject
)

// String renders the decision kind, including in example output.
func (k Kind) String() string {
	switch k {
	case Selected:
		return "Selected"
	case Executed:
		return "Executed"
	case Cancelled:
		return "Cancelled"
	case Reject:
		return "Reject"
	default:
		return "Unknown"
	}
}

// Decision is the result of handling one ASDU information object.
type Decision struct {
	Kind    Kind
	Command SingleCommand
	Reason  error
}

// Rejected reports whether the decision is a rejection.
func (d Decision) Rejected() bool { return d.Kind == Reject }

// Accepted reports whether the decision is a select, execute or cancel that
// the slave should acknowledge positively.
func (d Decision) Accepted() bool { return d.Kind != Reject }

// pendingSelection is one outstanding select bound to its connection.
type pendingSelection struct {
	conn     ConnID
	command  SingleCommand
	deadline time.Time
}

func (p pendingSelection) liveAt(now time.Time) bool {
	// A selection stays valid through its 30 second window and is void once
	// the deadline has been reached.
	return now.Before(p.deadline)
}

// StateMachine is the control-command state of a 104 slave. It owns the
// per-IOA select-before-execute bookkeeping and is safe for concurrent use
// across connections.
type StateMachine struct {
	mu            sync.Mutex
	points        map[IOA]bool // true means the point requires select first
	pending       map[IOA]pendingSelection
	now           func() time.Time
	timeout       time.Duration
	rejectUnknown bool
}

// Option configures a StateMachine.
type Option func(*StateMachine)

// WithClock injects a clock, which makes the 30 second timeout deterministic
// in tests. Production code leaves this unset and uses wall-clock time.
func WithClock(now func() time.Time) Option {
	return func(m *StateMachine) {
		if now != nil {
			m.now = now
		}
	}
}

// WithSelectTimeout overrides the select/execute window. The zero value keeps
// the standard 30 second timeout.
func WithSelectTimeout(d time.Duration) Option {
	return func(m *StateMachine) {
		if d > 0 {
			m.timeout = d
		}
	}
}

// RejectUnknownPoints makes the slave refuse every IOA not explicitly added
// with AddPoint. By default unknown points are accepted with direct execute.
func RejectUnknownPoints() Option {
	return func(m *StateMachine) { m.rejectUnknown = true }
}

// NewStateMachine builds an empty slave command state.
func NewStateMachine(opts ...Option) *StateMachine {
	m := &StateMachine{
		points:  make(map[IOA]bool),
		pending: make(map[IOA]pendingSelection),
		now:     time.Now,
		timeout: DefaultSelectTimeout,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// AddPoint registers an IOA. When selectFirst is true the point requires a
// select from the same connection before any execute; a direct execute is
// rejected. When false the point can be driven directly.
func (m *StateMachine) AddPoint(ioa IOA, selectFirst bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.points[ioa] = selectFirst
}

// Frame is the portion of an incoming ASDU the state machine needs.
type Frame struct {
	Type   TypeID
	Cause  COT
	Conn   ConnID
	Object []byte // one information object: 3 IOA octets + command octet
}

// HandleFrame is the entry point a 104 slave calls once per information
// object. Only single command (type 45) is understood.
func (m *StateMachine) HandleFrame(f Frame) (Decision, error) {
	if f.Type != TypeSingleCommand {
		return Decision{Kind: Reject, Reason: ErrUnsupportedType}, ErrUnsupportedType
	}
	cmd, err := DecodeSingleCommand(f.Object)
	if err != nil {
		return Decision{Kind: Reject, Reason: err}, err
	}
	return m.HandleSingleCommand(f.Conn, f.Cause, cmd), nil
}

// HandleSingleCommand applies one decoded single command. The returned
// Decision is never nil; inspect Kind and Reason instead of the error for
// protocol-level accept/reject decisions.
func (m *StateMachine) HandleSingleCommand(conn ConnID, cause COT, cmd SingleCommand) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()

	if cause == COTCancellation {
		return m.handleCancel(conn, cmd, now)
	}

	if cmd.Select {
		return m.handleSelect(conn, cmd, now)
	}
	return m.handleExecute(conn, cmd, now)
}

func (m *StateMachine) handleSelect(conn ConnID, cmd SingleCommand, now time.Time) Decision {
	if m.rejectUnknown {
		if _, known := m.points[cmd.IOA]; !known {
			return m.reject(cmd, ErrUnknownPoint)
		}
	}

	if sel, ok := m.pending[cmd.IOA]; ok {
		if !sel.liveAt(now) {
			// Stale entry no longer owns the IOA; this re-select replaces it.
			delete(m.pending, cmd.IOA)
		} else if sel.conn != conn {
			// Another connection currently holds this point; it must cancel
			// or time out before this connection may arm it.
			return m.reject(cmd, ErrConnectionMismatch)
		}
	}

	m.pending[cmd.IOA] = pendingSelection{
		conn:     conn,
		command:  cmd,
		deadline: now.Add(m.timeout),
	}
	return Decision{Kind: Selected, Command: cmd}
}

func (m *StateMachine) handleExecute(conn ConnID, cmd SingleCommand, now time.Time) Decision {
	selectFirst, known := m.points[cmd.IOA]
	if m.rejectUnknown && !known {
		return m.reject(cmd, ErrUnknownPoint)
	}

	sel, ok := m.pending[cmd.IOA]
	if !ok {
		if selectFirst {
			// Direct execute on a select-before-execute point, or an execute
			// whose select targeted a different IOA: nothing is armed here.
			return m.reject(cmd, ErrSelectRequired)
		}
		delete(m.pending, cmd.IOA)
		return Decision{Kind: Executed, Command: cmd}
	}

	if !sel.liveAt(now) {
		delete(m.pending, cmd.IOA)
		return m.reject(cmd, ErrSelectionExpired)
	}

	if sel.conn != conn {
		return m.reject(cmd, ErrConnectionMismatch)
	}
	if !sel.command.sameCommandValue(cmd) {
		return m.reject(cmd, ErrValueMismatch)
	}

	// Same connection, same IOA, same command value, within the window: the
	// execute confirms the selection. Consume the selection so it cannot be
	// replayed.
	delete(m.pending, cmd.IOA)
	return Decision{Kind: Executed, Command: cmd}
}

func (m *StateMachine) handleCancel(conn ConnID, cmd SingleCommand, now time.Time) Decision {
	if m.rejectUnknown {
		if _, known := m.points[cmd.IOA]; !known {
			return m.reject(cmd, ErrUnknownPoint)
		}
	}

	sel, ok := m.pending[cmd.IOA]
	if !ok {
		// Nothing armed: cancellation is a harmless no-op that is still
		// acknowledged.
		return Decision{Kind: Cancelled, Command: cmd}
	}
	if !sel.liveAt(now) {
		// Expired entry: nothing live to cancel.
		delete(m.pending, cmd.IOA)
		return Decision{Kind: Cancelled, Command: cmd}
	}
	if sel.conn != conn {
		// One connection must not be allowed to cancel another's selection.
		return m.reject(cmd, ErrConnectionMismatch)
	}

	delete(m.pending, cmd.IOA)
	return Decision{Kind: Cancelled, Command: cmd}
}

func (m *StateMachine) reject(cmd SingleCommand, reason error) Decision {
	return Decision{Kind: Reject, Command: cmd, Reason: reason}
}

// Selection is a read-only view of an outstanding select.
type Selection struct {
	Conn     ConnID
	Command  SingleCommand
	Deadline time.Time
}

// ActiveSelection reports the live selection for an IOA, if any. The boolean
// is false when the point is not selected or the selection has expired.
func (m *StateMachine) ActiveSelection(ioa IOA) (Selection, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sel, ok := m.pending[ioa]
	if !ok || !sel.liveAt(m.now()) {
		return Selection{}, false
	}
	return Selection{Conn: sel.conn, Command: sel.command, Deadline: sel.deadline}, true
}

// Sweep drops every expired selection. It is optional; selections also expire
// lazily on the next frame, but Sweep lets a slave run a periodic timer.
func (m *StateMachine) Sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	for ioa, sel := range m.pending {
		if !sel.liveAt(now) {
			delete(m.pending, ioa)
		}
	}
}
