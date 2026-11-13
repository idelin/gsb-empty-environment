package netsec

import (
	"fmt"
	"sync"
)

// State is the current phase of the TLS connection as tracked by the
// early-data enforcement machine.
type State int

const (
	// StateIdle is the initial state, before any handshake event.
	StateIdle State = iota

	// StateEarlyData means 0-RTT early data has been accepted and the
	// machine is counting incoming early-data bytes against the limit.
	StateEarlyData

	// StateHandshake means the early-data phase is over and the rest of the
	// TLS handshake is in progress.
	StateHandshake

	// StateConnected means the handshake completed; all subsequent
	// application data is ordinary 1-RTT data and is not limited.
	StateConnected

	// StateFailed is terminal. A limit breach or protocol violation occurred.
	// Early-data bytes at or beyond the limit are withheld and must never be
	// handed to the application.
	StateFailed

	// StateClosed is the clean terminal state after [EventClose].
	StateClosed
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "Idle"
	case StateEarlyData:
		return "EarlyData"
	case StateHandshake:
		return "Handshake"
	case StateConnected:
		return "Connected"
	case StateFailed:
		return "Failed"
	case StateClosed:
		return "Closed"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

// Event is a state transition supplied by the TLS stack. Concrete event
// types describe handshake progress and incoming record payloads.
//
// Implementations are intentionally value types; the machine does not retain
// the byte slices passed to it.
type Event interface {
	eventMarker()
}

// EventStart begins a new TLS connection. It must be the first event.
type EventStart struct {
	// MaxEarlyData is the max_early_data value advertised in the resumed
	// ticket. Zero means the server does not accept any 0-RTT data.
	MaxEarlyData uint64
}

// EventBeginEarlyData signals that the server accepted the 0-RTT extension.
// Only after this event may [EventEarlyData] records be delivered.
type EventBeginEarlyData struct{}

// EventRejectEarlyData signals that the server declined 0-RTT (for example by
// responding with a normal ClientHello/ServerHello). Any early data already
// received is treated as discarded and the connection continues as 1-RTT.
type EventRejectEarlyData struct{}

// EventEndOfEarlyData signals the end_of_early_data boundary and moves the
// connection into the main handshake.
type EventEndOfEarlyData struct{}

// EventHandshakeComplete signals that the TLS handshake finished.
type EventHandshakeComplete struct{}

// EventEarlyData carries one decrypted 0-RTT application-data record.
type EventEarlyData struct {
	Payload []byte
}

// EventApplicationData carries one decrypted 1-RTT application-data record.
type EventApplicationData struct {
	Payload []byte
}

// EventAlert signals a fatal TLS alert from the peer.
type EventAlert struct {
	// AlertDescription is the TLS alert description code, purely informational.
	AlertDescription uint8
}

// EventClose is an orderly connection shutdown.
type EventClose struct{}

func (EventStart) eventMarker()             {}
func (EventBeginEarlyData) eventMarker()    {}
func (EventRejectEarlyData) eventMarker()   {}
func (EventEndOfEarlyData) eventMarker()    {}
func (EventHandshakeComplete) eventMarker() {}
func (EventEarlyData) eventMarker()         {}
func (EventApplicationData) eventMarker()   {}
func (EventAlert) eventMarker()             {}
func (EventClose) eventMarker()             {}

// ActionKind classifies an [Action] returned by [Machine.Handle].
type ActionKind int

const (
	// ActionDeliver: the payload is permitted and must be handed to the
	// application. During the early-data phase it is guaranteed to fit within
	// max_early_data.
	ActionDeliver ActionKind = iota

	// ActionWithhold: the payload (or its over-limit suffix) must be dropped
	// and never delivered to the application.
	ActionWithhold

	// ActionFail: a limit breach or protocol violation occurred. The TLS
	// stack must abort the handshake/connection. Any accompanying Withheld
	// bytes are not deliverable.
	ActionFail

	// ActionDrop: 0-RTT data was rejected by the server; the decrypted bytes
	// are discarded rather than delivered.
	ActionDrop
)

// String returns a human-readable name for the action kind.
func (k ActionKind) String() string {
	switch k {
	case ActionDeliver:
		return "Deliver"
	case ActionWithhold:
		return "Withhold"
	case ActionFail:
		return "Fail"
	case ActionDrop:
		return "Drop"
	default:
		return fmt.Sprintf("Unknown(%d)", int(k))
	}
}

// Action is a single directive the machine emits in response to an event.
//
// For [ActionDeliver], Payload holds the bytes the application may receive.
// For [ActionWithhold] and [ActionFail], WithheldBytes is the number of bytes
// that were dropped and Payload is always nil.
type Action struct {
	Kind          ActionKind
	Payload       []byte
	WithheldBytes uint64
	Reason        error
}

// Machine is an importable, goroutine-safe state machine that enforces the
// ticket's max_early_data byte ceiling on cumulative 0-RTT data.
//
// The TLS stack feeds connection events to [Machine.Handle] and applies the
// returned actions to the application-facing read path. The guarantee is:
//
//   - Early-data bytes whose cumulative length stays within max_early_data
//     produce [ActionDeliver] and may reach the application.
//   - The first record that would cross the limit is split: its in-limit
//     prefix is delivered and every remaining byte is withheld.
//   - Once the limit is exceeded the machine enters [StateFailed]; all further
//     early data is withheld, so already-decrypted excess bytes can never be
//     delivered.
type Machine struct {
	mu            sync.Mutex
	state         State
	maxEarlyData  uint64
	earlyAccepted uint64
	earlyWithheld uint64
	earlyDropped  uint64
	failErr       error
}

// New returns a machine in [StateIdle].
func New() *Machine {
	return &Machine{state: StateIdle}
}

// State returns the current machine state.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Err returns the error that moved the machine to [StateFailed], or nil.
func (m *Machine) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failErr
}

// MaxEarlyData returns the limit negotiated for this connection (0 before
// [EventStart]).
func (m *Machine) MaxEarlyData() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxEarlyData
}

// EarlyAccepted returns the cumulative number of early-data bytes delivered.
func (m *Machine) EarlyAccepted() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyAccepted
}

// EarlyWithheld returns the cumulative number of early-data bytes that crossed
// the limit and were withheld from the application.
func (m *Machine) EarlyWithheld() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyWithheld
}

// EarlyDropped returns the cumulative number of early-data bytes discarded
// because the server rejected 0-RTT.
func (m *Machine) EarlyDropped() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyDropped
}

// Handle applies one TLS-stack event and returns the directives to apply.
//
// A non-nil error means the event violated the limit or protocol; the machine
// has moved to [StateFailed]. Inspect the returned actions (not the error) to
// decide which bytes reach the application: only [ActionDeliver] payloads are
// deliverable.
func (m *Machine) Handle(ev Event) ([]Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ev == nil {
		return m.failLocked(ErrNilEvent)
	}

	switch e := ev.(type) {
	case EventStart:
		return m.onStart(e)
	case EventBeginEarlyData:
		return m.onBeginEarlyData()
	case EventRejectEarlyData:
		return m.onRejectEarlyData()
	case EventEndOfEarlyData:
		return m.onEndOfEarlyData()
	case EventHandshakeComplete:
		return m.onHandshakeComplete()
	case EventEarlyData:
		return m.onEarlyData(e)
	case EventApplicationData:
		return m.onApplicationData(e)
	case EventAlert:
		return m.onAlert(e)
	case EventClose:
		return m.onClose()
	default:
		return m.failLocked(fmt.Errorf("%w: %T", ErrUnexpectedEvent, ev))
	}
}

func (m *Machine) onStart(e EventStart) ([]Action, error) {
	if m.state != StateIdle {
		return m.unexpected(e)
	}
	m.maxEarlyData = e.MaxEarlyData
	m.state = StateHandshake
	return nil, nil
}

func (m *Machine) onBeginEarlyData() ([]Action, error) {
	if m.state != StateHandshake {
		return m.unexpected(EventBeginEarlyData{})
	}
	m.state = StateEarlyData
	return nil, nil
}

func (m *Machine) onRejectEarlyData() ([]Action, error) {
	if m.state != StateHandshake && m.state != StateEarlyData {
		return m.unexpected(EventRejectEarlyData{})
	}
	if m.state == StateEarlyData {
		// Decrypted 0-RTT records received before the rejection become
		// ordinary discarded data rather than delivered application data.
		m.earlyDropped += m.earlyAccepted
		m.earlyAccepted = 0
	}
	m.state = StateHandshake
	return nil, nil
}

func (m *Machine) onEndOfEarlyData() ([]Action, error) {
	if m.state != StateEarlyData {
		return m.unexpected(EventEndOfEarlyData{})
	}
	m.state = StateHandshake
	return nil, nil
}

func (m *Machine) onHandshakeComplete() ([]Action, error) {
	if m.state != StateHandshake {
		return m.unexpected(EventHandshakeComplete{})
	}
	m.state = StateConnected
	return nil, nil
}

func (m *Machine) onEarlyData(e EventEarlyData) ([]Action, error) {
	switch m.state {
	case StateFailed:
		// Fail closed: never deliver any further early-data bytes.
		n := uint64(len(e.Payload))
		m.earlyWithheld += n
		return []Action{{Kind: ActionWithhold, WithheldBytes: n, Reason: m.failErr}}, nil
	case StateEarlyData:
		// Accepted path; enforce the cumulative byte ceiling below.
	default:
		return m.failLocked(fmt.Errorf("%w: early data before 0-RTT was accepted", ErrNoEarlyData))
	}

	remaining := m.maxEarlyData - m.earlyAccepted
	n := uint64(len(e.Payload))

	if n <= remaining {
		m.earlyAccepted += n
		return []Action{{Kind: ActionDeliver, Payload: e.Payload}}, nil
	}

	// The record crosses the ceiling. Deliver the in-limit prefix (if any),
	// withhold every over-limit byte, and fail the connection.
	allowed := remaining
	over := n - allowed
	m.earlyAccepted += allowed
	m.earlyWithheld += over

	actions := make([]Action, 0, 2)
	if allowed > 0 {
		actions = append(actions, Action{Kind: ActionDeliver, Payload: e.Payload[:allowed]})
	}
	actions = append(actions, Action{
		Kind:          ActionFail,
		WithheldBytes: over,
		Reason: fmt.Errorf("%w: accepted=%d limit=%d record=%d",
			ErrEarlyDataLimitExceeded, m.earlyAccepted, m.maxEarlyData, n),
	})
	m.state = StateFailed
	m.failErr = ErrEarlyDataLimitExceeded
	return actions, ErrEarlyDataLimitExceeded
}

func (m *Machine) onApplicationData(e EventApplicationData) ([]Action, error) {
	switch m.state {
	case StateConnected:
		return []Action{{Kind: ActionDeliver, Payload: e.Payload}}, nil
	case StateFailed:
		n := uint64(len(e.Payload))
		return []Action{{Kind: ActionWithhold, WithheldBytes: n, Reason: m.failErr}}, nil
	default:
		return m.failLocked(fmt.Errorf("%w: application data before handshake completion", ErrUnexpectedEvent))
	}
}

func (m *Machine) onAlert(e EventAlert) ([]Action, error) {
	if m.state == StateClosed {
		return m.unexpected(e)
	}
	err := fmt.Errorf("%w: alert=%d", ErrConnectionFailed, e.AlertDescription)
	return m.failLocked(err)
}

func (m *Machine) onClose() ([]Action, error) {
	if m.state == StateFailed {
		m.state = StateClosed
		return nil, nil
	}
	if m.state == StateClosed {
		return m.unexpected(EventClose{})
	}
	m.state = StateClosed
	return nil, nil
}

func (m *Machine) unexpected(ev Event) ([]Action, error) {
	return m.failLocked(fmt.Errorf("%w: %T in state %s", ErrUnexpectedEvent, ev, m.state))
}

func (m *Machine) failLocked(err error) ([]Action, error) {
	m.state = StateFailed
	m.failErr = err
	return []Action{{Kind: ActionFail, Reason: err}}, err
}
