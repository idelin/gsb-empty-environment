package netsec

import "fmt"

// Phase is the login phase of the device protocol.
type Phase int

const (
	PhaseIdle Phase = iota
	PhaseChallenge
	PhaseAuthenticated
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseChallenge:
		return "challenge"
	case PhaseAuthenticated:
		return "authenticated"
	}
	return fmt.Sprintf("phase(%d)", int(p))
}

// State is the complete externally visible protocol state. It is a plain
// comparable value so it can be serialized deterministically and used as a
// map key by the explorer.
type State struct {
	Phase    Phase `json:"phase"`
	Session  uint8 `json:"session"`
	AuthFlag bool  `json:"authFlag"`
}

// InitialState is the power-on state: idle, session 0, authFlag cleared.
func InitialState() State {
	return State{Phase: PhaseIdle, Session: 0, AuthFlag: false}
}

// Validate reports whether the state is inside the modelled domain:
// a known phase and a 2-bit session number.
func (s State) Validate() error {
	if s.Phase < PhaseIdle || s.Phase > PhaseAuthenticated {
		return fmt.Errorf("netsec: invalid phase %d", int(s.Phase))
	}
	if s.Session > 3 {
		return fmt.Errorf("netsec: session %d out of 2-bit range", s.Session)
	}
	return nil
}

func (s State) String() string {
	return fmt.Sprintf("{phase:%s session:%d authFlag:%t}", s.Phase, s.Session, s.AuthFlag)
}

// Kind enumerates the five protocol message types.
type Kind int

const (
	KindBegin Kind = iota
	KindProof
	KindAbort
	KindWrite
	KindReset
)

func (k Kind) String() string {
	switch k {
	case KindBegin:
		return "Begin"
	case KindProof:
		return "Proof"
	case KindAbort:
		return "Abort"
	case KindWrite:
		return "Write"
	case KindReset:
		return "Reset"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Message is one protocol message. SigValid is the fixed signature bit
// carried by Proof messages; the simulated verifier derives ok from it.
type Message struct {
	Kind     Kind  `json:"kind"`
	Session  uint8 `json:"session,omitempty"`
	SigValid bool  `json:"sigValid,omitempty"`
}

func Begin(s uint8) Message { return Message{Kind: KindBegin, Session: s} }

func Proof(s uint8, sigValid bool) Message {
	return Message{Kind: KindProof, Session: s, SigValid: sigValid}
}

func Abort(s uint8) Message { return Message{Kind: KindAbort, Session: s} }

func Write(s uint8) Message { return Message{Kind: KindWrite, Session: s} }

func Reset() Message { return Message{Kind: KindReset} }

func (m Message) String() string {
	switch m.Kind {
	case KindProof:
		return fmt.Sprintf("Proof(%d,%t)", m.Session, m.SigValid)
	case KindReset:
		return "Reset"
	default:
		return fmt.Sprintf("%s(%d)", m.Kind, m.Session)
	}
}

// OrderedMessages returns the full message alphabet in the mandated
// enumeration order: Begin, Proof with invalid signature, Proof with valid
// signature, Abort, Write, Reset; ties broken by ascending session number.
func OrderedMessages() []Message {
	msgs := make([]Message, 0, 21)
	for s := uint8(0); s < 4; s++ {
		msgs = append(msgs, Begin(s))
	}
	for s := uint8(0); s < 4; s++ {
		msgs = append(msgs, Proof(s, false))
	}
	for s := uint8(0); s < 4; s++ {
		msgs = append(msgs, Proof(s, true))
	}
	for s := uint8(0); s < 4; s++ {
		msgs = append(msgs, Abort(s))
	}
	for s := uint8(0); s < 4; s++ {
		msgs = append(msgs, Write(s))
	}
	msgs = append(msgs, Reset())
	return msgs
}

// Verifier is the simulated signature verifier. It judges the ok bit of a
// Proof message.
type Verifier interface {
	Verify(msg Message) bool
}

// VerifierFunc adapts a plain function to Verifier.
type VerifierFunc func(msg Message) bool

func (f VerifierFunc) Verify(msg Message) bool { return f(msg) }

// FixedBitVerifier is the default simulated verifier: ok is exactly the
// fixed signature bit carried inside the message.
type FixedBitVerifier struct{}

func (FixedBitVerifier) Verify(msg Message) bool { return msg.SigValid }
