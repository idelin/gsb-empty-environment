package netsec

import (
	"errors"
	"fmt"
)

// ErrRenegotiation is returned when a ClientHello arrives after the
// handshake has completed. TLS 1.3 forbids renegotiation, so the
// connection must fail instead of starting a new nested handshake.
var ErrRenegotiation = errors.New("tls: renegotiation is not supported in TLS 1.3")

// State is the connection phase tracked by the state machine.
type State int

const (
	// StateStart is the initial state; no handshake message seen yet.
	StateStart State = iota
	// StateHandshaking means the initial handshake is in progress.
	StateHandshaking
	// StateEstablished means the handshake finished and the connection
	// carries application data.
	StateEstablished
	// StateFailed means a fatal protocol error occurred; the connection
	// is dead and no further handshake may begin.
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateStart:
		return "Start"
	case StateHandshaking:
		return "Handshaking"
	case StateEstablished:
		return "Established"
	case StateFailed:
		return "Failed"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Event is a handshake-relevant message delivered by the TLS stack.
type Event int

const (
	EventClientHello Event = iota
	EventServerHello
	EventFinished
	EventApplicationData
)

func (e Event) String() string {
	switch e {
	case EventClientHello:
		return "ClientHello"
	case EventServerHello:
		return "ServerHello"
	case EventFinished:
		return "Finished"
	case EventApplicationData:
		return "ApplicationData"
	default:
		return fmt.Sprintf("Event(%d)", int(e))
	}
}

// Machine is a TLS 1.3 handshake state machine fed by events from the
// TLS stack. It is not safe for concurrent use.
type Machine struct {
	state State
	err   error
}

// New returns a Machine in StateStart.
func New() *Machine {
	return &Machine{state: StateStart}
}

// State reports the current state.
func (m *Machine) State() State {
	return m.state
}

// Err reports the fatal error that moved the machine to StateFailed,
// or nil if it has not failed.
func (m *Machine) Err() error {
	return m.err
}

// Handle processes one event from the TLS stack. A ClientHello that
// opens the initial handshake is accepted; a ClientHello arriving
// after Finished is renegotiation and fails the connection with
// ErrRenegotiation. Once failed, the machine stays in StateFailed and
// rejects every further event, so no nested handshake can begin.
func (m *Machine) Handle(ev Event) error {
	switch m.state {
	case StateStart:
		if ev == EventClientHello {
			m.state = StateHandshaking
			return nil
		}
		return m.fail(fmt.Errorf("tls: unexpected %s in state %s", ev, m.state))
	case StateHandshaking:
		switch ev {
		case EventServerHello, EventClientHello:
			// A second ClientHello mid-handshake answers
			// HelloRetryRequest and stays in the same handshake.
			return nil
		case EventFinished:
			m.state = StateEstablished
			return nil
		default:
			return m.fail(fmt.Errorf("tls: unexpected %s in state %s", ev, m.state))
		}
	case StateEstablished:
		if ev == EventClientHello {
			return m.fail(ErrRenegotiation)
		}
		return nil
	case StateFailed:
		return m.err
	default:
		return m.fail(fmt.Errorf("tls: invalid state %d", int(m.state)))
	}
}

func (m *Machine) fail(err error) error {
	m.state = StateFailed
	m.err = err
	return err
}
