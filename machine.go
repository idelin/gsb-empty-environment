package netsec

import "fmt"

// StateMachine validates the order of the client side of TLS client
// authentication. It is safe to feed it states as they are reported by a TLS
// stack; unrelated handshake states are ignored.
//
// After a CertificateRequest, the only valid sequence is:
//
//	Certificate -> CertificateVerify -> Finished
//
// In particular, Finished may not skip either client-authentication message.
type StateMachine struct {
	state State
	err   error
}

// HandshakeStateMachine, ClientAuthStateMachine, FSM, and Machine are aliases
// for StateMachine.
type (
	HandshakeStateMachine  = StateMachine
	ClientAuthStateMachine = StateMachine
	FSM                    = StateMachine
	Machine                = StateMachine
)

// NewStateMachine returns a machine ready to receive TLS handshake states.
// Optional initial events are applied in order.
func NewStateMachine(initial ...any) *StateMachine {
	m := &StateMachine{state: Start}
	for _, event := range initial {
		_ = m.Step(event)
	}
	return m
}

// New, NewFSM, NewHandshakeStateMachine, and NewClientAuthStateMachine are
// equivalent constructors for integration code with different naming styles.
func New(initial ...any) *StateMachine {
	return NewStateMachine(initial...)
}

func NewFSM(initial ...any) *StateMachine {
	return NewStateMachine(initial...)
}

func NewHandshakeStateMachine(initial ...any) *StateMachine {
	return NewStateMachine(initial...)
}

func NewClientAuthStateMachine(initial ...any) *StateMachine {
	return NewStateMachine(initial...)
}

func NewClientAuthFSM(initial ...any) *StateMachine {
	return NewStateMachine(initial...)
}

func NewMachine(initial ...any) *StateMachine {
	return NewStateMachine(initial...)
}

// State returns the current state. Before any event it is Start; after the
// ordered Certificate, CertificateVerify and Finished events it is Finished.
func (m *StateMachine) State() State {
	if m == nil || m.state == "" {
		return Start
	}
	return m.state
}

// CurrentState and Current are aliases for State.
func (m *StateMachine) CurrentState() State {
	return m.State()
}

func (m *StateMachine) Current() State {
	return m.State()
}

// Step applies one message or state reported by the TLS stack. It returns an
// error, without changing state, if the event would skip Certificate or
// CertificateVerify after a CertificateRequest.
func (m *StateMachine) Step(event any) error {
	if m == nil {
		return fmt.Errorf("%w: nil state machine", ErrUnexpectedEvent)
	}
	if m.state == "" {
		m.state = Start
	}

	parsed, known, err := parseEvent(event)
	if err != nil {
		m.err = err
		return err
	}
	if !known {
		return nil
	}

	switch parsed {
	case Start:
		return nil

	case CertificateRequest:
		// A post-handshake CertificateRequest starts a new client-authentication
		// exchange, so it is valid even after an earlier exchange completed.
		m.state = CertificateRequest
		m.err = nil
		return nil

	case Certificate:
		if m.state != CertificateRequest {
			return m.fail(unexpected(parsed, m.state))
		}
		m.state = Certificate
		return nil

	case CertificateVerify:
		switch m.state {
		case Certificate:
			m.state = CertificateVerify
			return nil
		case CertificateRequest:
			return m.fail(fmt.Errorf("%w: received CertificateVerify before Certificate: %w",
				ErrUnexpectedEvent, ErrCertificateRequired))
		default:
			return m.fail(unexpected(parsed, m.state))
		}

	case Finished:
		switch m.state {
		case CertificateRequest:
			return m.fail(fmt.Errorf("%w: received Finished before Certificate: %w",
				ErrUnexpectedEvent, ErrCertificateRequired))
		case Certificate:
			return m.fail(fmt.Errorf("%w: received Finished before CertificateVerify: %w",
				ErrUnexpectedEvent, ErrCertificateVerifyRequired))
		case Start, CertificateVerify:
			// Finished is valid at Start when the server did not request client
			// authentication, and after CertificateVerify when it did.
			m.state = Finished
			m.err = nil
			return nil
		case Finished:
			return m.fail(ErrHandshakeComplete)
		}
	}

	return m.fail(unexpected(parsed, m.state))
}

// Feed, Handle, Transition, and Advance are aliases for Step.
func (m *StateMachine) Feed(event any) error {
	return m.Step(event)
}

func (m *StateMachine) Handle(event any) error {
	return m.Step(event)
}

func (m *StateMachine) Transition(event any) error {
	return m.Step(event)
}

func (m *StateMachine) Advance(event any) error {
	return m.Step(event)
}

// Observe and Receive are aliases for Step for stacks that report observed
// inbound messages rather than state transitions.
func (m *StateMachine) Observe(event any) error {
	return m.Step(event)
}

func (m *StateMachine) Receive(event any) error {
	return m.Step(event)
}

// OnCertificateRequest, OnCertificate, OnCertificateVerify, and OnFinished
// allow a stack to report messages through named callbacks. Payload arguments
// such as the certificate list or signature are intentionally ignored.
func (m *StateMachine) OnCertificateRequest(_ ...any) error {
	return m.Step(CertificateRequest)
}

func (m *StateMachine) OnCertificate(_ ...any) error {
	return m.Step(Certificate)
}

func (m *StateMachine) OnCertificateVerify(_ ...any) error {
	return m.Step(CertificateVerify)
}

func (m *StateMachine) OnFinished(_ ...any) error {
	return m.Step(Finished)
}

// Validate reports whether every event applied so far was in a valid order.
// A sequence may end after CertificateVerify, but not while a requested
// Certificate or CertificateVerify is still outstanding.
func (m *StateMachine) Validate() error {
	if m == nil {
		return fmt.Errorf("%w: nil state machine", ErrUnexpectedEvent)
	}
	if m.err != nil {
		return m.err
	}
	switch m.state {
	case CertificateRequest:
		return ErrCertificateRequired
	case Certificate:
		return ErrCertificateVerifyRequired
	default:
		return nil
	}
}

// Valid reports whether no invalid transition has occurred.
func (m *StateMachine) Valid() bool {
	return m != nil && m.err == nil
}

func (m *StateMachine) IsValid() bool {
	return m.Valid()
}

// Err returns the last transition error, if any.
func (m *StateMachine) Err() error {
	if m == nil {
		return fmt.Errorf("%w: nil state machine", ErrUnexpectedEvent)
	}
	return m.err
}

// Complete reports whether a client-authentication exchange reached Finished.
func (m *StateMachine) Complete() bool {
	return m != nil && m.state == Finished
}

func (m *StateMachine) IsComplete() bool {
	return m.Complete()
}

// Reset discards the current exchange and returns the machine to Start.
func (m *StateMachine) Reset() {
	if m != nil {
		m.state = Start
		m.err = nil
	}
}

func (m *StateMachine) fail(err error) error {
	m.err = err
	return err
}
