package netsec

import "errors"

// ErrSecondHelloRetryRequest is returned when a handshake sees a second
// HelloRetryRequest; a handshake permits at most one.
var ErrSecondHelloRetryRequest = errors.New("netsec: second HelloRetryRequest in one handshake")

// State is a handshake state fed in by the TLS stack.
type State int

const (
	StateStart State = iota
	StateClientHello
	StateHelloRetryRequest
	StateServerHello
	StateFinished
)

// Machine tracks a single TLS handshake, enforcing at most one
// HelloRetryRequest per handshake.
type Machine struct {
	state   State
	retried bool
}

// New returns a Machine in its initial state.
func New() *Machine {
	return &Machine{state: StateStart}
}

// State returns the current state.
func (m *Machine) State() State {
	return m.state
}

// Advance consumes the next state from the TLS stack. A second
// HelloRetryRequest fails immediately; a handshake that completes after a
// single retry is accepted.
func (m *Machine) Advance(s State) error {
	if s == StateHelloRetryRequest {
		if m.retried {
			return ErrSecondHelloRetryRequest
		}
		m.retried = true
	}
	m.state = s
	return nil
}
