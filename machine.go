package streamguard

import "fmt"

// State is a named phase in the defended XMPP-style handshake.
type State string

const (
	StateInitial              State = "Initial"
	StateStreamOpen           State = "StreamOpen"
	StateStartTLS             State = "StartTLS"
	StateResetBufferAndStream State = "ResetBufferAndStream"
	StateSASLAuth             State = "SASLAuth"
	StateResourceBind         State = "ResourceBind"
	StateReady                State = "Ready"
	StateClosed               State = "Closed"
)

// Machine enforces the ordered handshake. It is not safe for concurrent use.
type Machine struct {
	cfg Config

	state State
	tls   bool

	secureStreamOpen  bool
	saslStarted       bool
	saslAuthenticated bool
	resourceBound     bool
	mechanism         string
	resource          string
}

// NewMachine creates a strict handshake state machine.
func NewMachine(cfgs ...Config) *Machine {
	cfg := DefaultConfig()
	if len(cfgs) > 0 {
		cfg = cfgs[0].normalize()
	}
	return &Machine{cfg: cfg, state: StateInitial}
}

// State returns the current state.
func (m *Machine) State() State { return m.state }

// IsTLS reports whether StartTLS has been upgraded.
func (m *Machine) IsTLS() bool { return m.tls }

// SecureStreamOpen reports whether a fresh stream header was accepted after
// the StartTLS buffer reset.
func (m *Machine) SecureStreamOpen() bool { return m.secureStreamOpen }

// SASLAuthenticated reports whether the host completed SASL.
func (m *Machine) SASLAuthenticated() bool { return m.saslAuthenticated }

// ResourceBound reports whether resource binding was acknowledged.
func (m *Machine) ResourceBound() bool { return m.resourceBound }

// Mechanism returns the supplied SASL mechanism name.
func (m *Machine) Mechanism() string { return m.mechanism }

// RequestedResource returns the supplied bind resource.
func (m *Machine) RequestedResource() string { return m.resource }

func stateRank(state State) int {
	switch state {
	case StateInitial:
		return 0
	case StateStreamOpen:
		return 1
	case StateStartTLS:
		return 2
	case StateResetBufferAndStream:
		return 3
	case StateSASLAuth:
		return 4
	case StateResourceBind:
		return 5
	case StateReady:
		return 6
	case StateClosed:
		return 7
	default:
		return -1
	}
}

// Transition performs an exact adjacent transition in the required order.
// Skipping phases, moving backwards, or jumping directly to SASL is rejected.
func (m *Machine) Transition(next State) error {
	currentRank := stateRank(m.state)
	nextRank := stateRank(next)
	if next == StateClosed {
		m.state = StateClosed
		return nil
	}
	if currentRank < 0 || nextRank != currentRank+1 {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidState, m.state, next)
	}

	m.state = next
	switch next {
	case StateResetBufferAndStream:
		m.tls = true
	case StateSASLAuth:
		m.saslStarted = true
	case StateReady:
		m.resourceBound = true
	}
	return nil
}

// StreamOpen advances directly to StreamOpen.
func (m *Machine) StreamOpen() error {
	return m.Transition(StateStreamOpen)
}

// StartTLS records the StartTLS request phase.
func (m *Machine) StartTLS() error {
	return m.Transition(StateStartTLS)
}

// UpgradeTLS marks the TLS upgrade boundary. Buffer ownership is handled by
// Guard; using this low-level method only advances the state machine.
func (m *Machine) UpgradeTLS() error {
	if m.state != StateStartTLS {
		return fmt.Errorf("%w: TLS upgrade in state %s", ErrInvalidState, m.state)
	}
	return m.Transition(StateResetBufferAndStream)
}

// ResetBufferAndStream is the named transition for the post-Upgrade phase.
func (m *Machine) ResetBufferAndStream() error {
	return m.UpgradeTLS()
}

// SASLAuth advances to the SASL authentication phase.
func (m *Machine) SASLAuth(mechanism ...string) error {
	if err := m.Transition(StateSASLAuth); err != nil {
		return err
	}
	m.saslStarted = true
	if len(mechanism) > 0 && mechanism[0] != "" {
		m.mechanism = mechanism[0]
	}
	return nil
}

// SASLSuccess records that the external SASL exchange succeeded.
func (m *Machine) SASLSuccess() error {
	if m.state != StateSASLAuth {
		return fmt.Errorf("%w: SASL success in state %s", ErrInvalidState, m.state)
	}
	m.saslAuthenticated = true
	return nil
}

// ResourceBind advances to the resource-binding phase.
func (m *Machine) ResourceBind(resource ...string) error {
	if err := m.Transition(StateResourceBind); err != nil {
		return err
	}
	if len(resource) > 0 {
		m.resource = resource[0]
	}
	return nil
}

// BindSuccess marks binding complete and moves the machine to Ready.
func (m *Machine) BindSuccess() error {
	if m.state != StateResourceBind {
		return fmt.Errorf("%w: bind success in state %s", ErrInvalidState, m.state)
	}
	return m.Transition(StateReady)
}

// Close terminates the machine.
func (m *Machine) Close() error {
	m.state = StateClosed
	return nil
}

// Observe validates a parsed frame against the ordered state machine.
func (m *Machine) Observe(frame *Frame) error {
	if frame == nil {
		return fmt.Errorf("%w: nil frame", ErrInvalidState)
	}

	switch m.state {
	case StateInitial:
		if frame.Kind != FrameStreamOpen {
			return fmt.Errorf(
				"%w: first frame must be StreamOpen, got %s",
				ErrInvalidState,
				frame.Kind,
			)
		}
		return m.Transition(StateStreamOpen)

	case StateStreamOpen:
		switch frame.Kind {
		case FrameStartTLS:
			return m.Transition(StateStartTLS)
		case FrameSASLAuth:
			return ErrSASLBeforeTLS
		case FrameStreamClose:
			return m.Close()
		default:
			return fmt.Errorf("%w: frame %s", ErrStanzaBeforeTLS, frame.Kind)
		}

	case StateStartTLS:
		return ErrTLSUpgradePending

	case StateResetBufferAndStream:
		if frame.Kind == FrameStreamOpen {
			m.secureStreamOpen = true
			return nil
		}
		if !m.secureStreamOpen {
			return ErrStreamRestartRequired
		}
		if frame.Kind == FrameSASLAuth {
			m.saslStarted = true
			return m.Transition(StateSASLAuth)
		}
		return fmt.Errorf("%w: expected SASLAuth, got %s", ErrSASLRequired, frame.Kind)

	case StateSASLAuth:
		switch frame.Kind {
		case FrameSASLAuth:
			m.saslStarted = true
			return nil
		case FrameStreamOpen:
			// RFC 6120 allows a second stream restart after SASL success.
			m.secureStreamOpen = true
			return nil
		case FrameResourceBind:
			if !m.saslAuthenticated {
				return ErrSASLNotComplete
			}
			return m.Transition(StateResourceBind)
		case FrameStreamClose:
			return m.Close()
		default:
			if !m.saslAuthenticated {
				return ErrSASLNotComplete
			}
			return fmt.Errorf("%w: got %s", ErrBindRequired, frame.Kind)
		}

	case StateResourceBind:
		switch frame.Kind {
		case FrameResourceBind:
			return nil
		case FrameStanza, FrameUnknown:
			return nil
		case FrameStreamClose:
			return m.Close()
		default:
			return fmt.Errorf("%w: unexpected frame %s during resource bind", ErrInvalidState, frame.Kind)
		}

	case StateReady:
		if frame.Kind == FrameStreamClose {
			return m.Close()
		}
		return nil

	case StateClosed:
		return ErrClosed

	default:
		return ErrInvalidState
	}
}
