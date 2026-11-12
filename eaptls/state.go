package eaptls

import (
	"context"
	"errors"
	"sync"
)

// Phase is the state of the EAP-TLS client state machine.
type Phase int

const (
	// PhaseNew is the state before [State.Begin].
	PhaseNew Phase = iota
	// PhaseRunning means the TLS handshake is in progress.
	PhaseRunning
	// PhaseCommitted means the empty commitment message has been sent.
	PhaseCommitted
	// PhaseSucceeded means a valid EAP-Success has been accepted.
	PhaseSucceeded
	// PhaseFailed means an EAP-Failure has been received.
	PhaseFailed
)

func (p Phase) String() string {
	switch p {
	case PhaseNew:
		return "new"
	case PhaseRunning:
		return "running"
	case PhaseCommitted:
		return "committed"
	case PhaseSucceeded:
		return "succeeded"
	case PhaseFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// IdentityProvider returns the original user identity for the
// authentication. It is consulted when the handshake begins and again
// whenever a handshake is resumed.
type IdentityProvider interface {
	Identity(ctx context.Context) (string, error)
}

// Authorizer checks whether the given identity is currently authorized to
// complete the authentication. A non-nil error (for example, a revoked
// identity) blocks the handshake.
type Authorizer interface {
	Authorize(ctx context.Context, identity string) error
}

// Config wires the state machine to the supplicant's services.
type Config struct {
	Identities IdentityProvider
	Authz      Authorizer
}

// State is the EAP-TLS 1.3 client-side state machine. Its methods are safe
// for concurrent use.
type State struct {
	mu         sync.Mutex
	phase      Phase
	identity   string
	identities IdentityProvider
	authz      Authorizer

	lastIdentifier int // identifier of the most recent EAP-TLS request
	reassembly     []byte
	fragmenting    bool
	finalHandshake bool
}

// Protocol gating errors returned by the state machine.
var (
	// ErrEarlySuccess is returned when EAP-Success arrives before the final
	// handshake message and the empty commitment message have completed.
	ErrEarlySuccess = errors.New("eaptls: EAP-Success received before handshake commitment")
	// ErrFinalHandshakeNotReceived is returned when a commitment is requested
	// before the server's final TLS handshake message was observed.
	ErrFinalHandshakeNotReceived = errors.New("eaptls: final TLS handshake message not received")
	// ErrIdentityMismatch is returned when a resumed handshake re-fetches an
	// identity different from the original one.
	ErrIdentityMismatch = errors.New("eaptls: resumed identity does not match original identity")
	// ErrIdentifierMismatch is returned when an EAP-Success identifier does
	// not match the identifier of the last EAP request.
	ErrIdentifierMismatch = errors.New("eaptls: EAP-Success identifier mismatch")
	// ErrNotBegun is returned when a method callback runs before [State.Begin].
	ErrNotBegun = errors.New("eaptls: handshake has not begun")
	// ErrAlreadyBegun is returned when [State.Begin] is called twice.
	ErrAlreadyBegun = errors.New("eaptls: handshake has already begun")
	// ErrStateTerminal is returned for callbacks after success or failure.
	ErrStateTerminal = errors.New("eaptls: state machine is in a terminal phase")
	// ErrNotRequest is returned when [State.HandleRequest] gets a non-request.
	ErrNotRequest = errors.New("eaptls: packet is not an EAP-Request")
	// ErrNotSuccess is returned when [State.HandleSuccess] gets a non-success.
	ErrNotSuccess = errors.New("eaptls: packet is not an EAP-Success")
	// ErrNotFailure is returned when [State.HandleFailure] gets a non-failure.
	ErrNotFailure = errors.New("eaptls: packet is not an EAP-Failure")
	// ErrUnsupportedEAPType is returned for non-EAP-TLS requests.
	ErrUnsupportedEAPType = errors.New("eaptls: unsupported EAP type")
	// ErrServerStart is returned when a request carries the EAP-TLS Start flag.
	ErrServerStart = errors.New("eaptls: Start flag is only valid on responses")
	// ErrMoreFragments indicates a fragment was stored; more are expected.
	ErrMoreFragments = errors.New("eaptls: more EAP-TLS fragments expected")
	// ErrNoRequestIdentifier is returned when no request identifier is known.
	ErrNoRequestIdentifier = errors.New("eaptls: no EAP request identifier recorded")
)

// New creates a state machine from the supplied configuration.
func New(cfg Config) (*State, error) {
	if cfg.Identities == nil || cfg.Authz == nil {
		return nil, errors.New("eaptls: identity provider and authorizer are required")
	}
	return &State{
		phase:          PhaseNew,
		identities:     cfg.Identities,
		authz:          cfg.Authz,
		lastIdentifier: -1,
	}, nil
}

// Begin loads the original user identity and verifies its authorization
// before any EAP-TLS exchange is processed.
func (s *State) Begin(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseNew {
		return "", ErrAlreadyBegun
	}
	identity, err := s.identities.Identity(ctx)
	if err != nil {
		return "", err
	}
	if err := s.authz.Authorize(ctx, identity); err != nil {
		return "", err
	}
	s.identity = identity
	s.phase = PhaseRunning
	return identity, nil
}

// HandleRequest processes an inbound EAP-Request packet, reassembling EAP-TLS
// fragments. For the final fragment it returns the full TLS record stream for
// the EAP method's TLS stack. Intermediate fragments return [ErrMoreFragments].
func (s *State) HandleRequest(packet []byte) ([]byte, error) {
	pkt, err := ParsePacket(packet)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseRunning && s.phase != PhaseCommitted {
		return nil, ErrStateTerminal
	}
	if pkt.Code != CodeRequest {
		return nil, ErrNotRequest
	}
	if pkt.Type != TypeEAPTLS {
		return nil, ErrUnsupportedEAPType
	}
	if pkt.Flags&flagStart != 0 {
		return nil, ErrServerStart
	}

	if pkt.Flags&flagMoreFragments != 0 {
		s.reassembly = append(s.reassembly, pkt.Data...)
		s.fragmenting = true
		s.lastIdentifier = int(pkt.Identifier)
		return nil, ErrMoreFragments
	}

	var data []byte
	if s.fragmenting {
		s.reassembly = append(s.reassembly, pkt.Data...)
		data = s.reassembly
		s.fragmenting = false
	} else {
		data = pkt.Data
	}
	s.reassembly = nil

	s.lastIdentifier = int(pkt.Identifier)
	if containsPlaintextFinished(data) {
		s.finalHandshake = true
	}
	return data, nil
}

// ObserveTLSData feeds TLS records exposed outside the EAP-TLS transport to
// the state machine. A plaintext Finished record marks the final handshake.
func (s *State) ObserveTLSData(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseRunning && s.phase != PhaseCommitted {
		return ErrStateTerminal
	}
	if containsPlaintextFinished(data) {
		s.finalHandshake = true
	}
	return nil
}

// MarkFinalHandshake records that the server's final TLS handshake message
// (the TLS Finished message) has been received and verified by the EAP
// method's TLS stack. It is idempotent.
func (s *State) MarkFinalHandshake() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseRunning && s.phase != PhaseCommitted {
		return ErrStateTerminal
	}
	s.finalHandshake = true
	return nil
}

// Commitment returns the empty EAP-TLS response that commits the peer after
// the final handshake message. It fails until [State.MarkFinalHandshake] (or
// plaintext Finished auto-detection) has recorded the final handshake.
func (s *State) Commitment() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == PhaseNew {
		return nil, ErrNotBegun
	}
	if s.phase == PhaseSucceeded || s.phase == PhaseFailed {
		return nil, ErrStateTerminal
	}
	if s.phase == PhaseCommitted {
		return nil, ErrStateTerminal
	}
	if !s.finalHandshake {
		return nil, ErrFinalHandshakeNotReceived
	}
	if s.lastIdentifier < 0 {
		return nil, ErrNoRequestIdentifier
	}
	frame := CommitmentFrame(byte(s.lastIdentifier))
	s.phase = PhaseCommitted
	return frame, nil
}

// HandleSuccess validates an inbound EAP-Success. Success is accepted only
// after the final handshake message was received and the empty commitment
// message was sent; a premature or spoofed success returns [ErrEarlySuccess].
func (s *State) HandleSuccess(packet []byte) error {
	pkt, err := ParsePacket(packet)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if pkt.Code != CodeSuccess {
		return ErrNotSuccess
	}
	if s.lastIdentifier >= 0 && pkt.Identifier != byte(s.lastIdentifier) {
		return ErrIdentifierMismatch
	}
	if s.phase == PhaseCommitted || s.phase == PhaseSucceeded {
		s.phase = PhaseSucceeded
		return nil
	}
	return ErrEarlySuccess
}

// HandleFailure records an inbound EAP-Failure. Failure terminates the
// authentication in every non-terminal phase.
func (s *State) HandleFailure(packet []byte) error {
	pkt, err := ParsePacket(packet)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if pkt.Code != CodeFailure {
		return ErrNotFailure
	}
	s.phase = PhaseFailed
	return nil
}

// Resume prepares a resumable handshake by re-fetching the original user
// identity and re-running authorization. It fails if the identity changed or
// is no longer authorized (for example, because it was revoked).
func (s *State) Resume(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == PhaseNew {
		return "", ErrNotBegun
	}
	if s.phase == PhaseSucceeded || s.phase == PhaseFailed {
		return "", ErrStateTerminal
	}

	// Tear down any prior TLS progress: a resume starts a fresh handshake,
	// so stale final-handshake or commitment state must never carry over.
	s.phase = PhaseRunning
	s.finalHandshake = false
	s.fragmenting = false
	s.reassembly = nil
	s.lastIdentifier = -1

	identity, err := s.identities.Identity(ctx)
	if err != nil {
		return "", err
	}
	if identity != s.identity {
		return "", ErrIdentityMismatch
	}
	if err := s.authz.Authorize(ctx, identity); err != nil {
		return "", err
	}
	return identity, nil
}

// Identity returns the original user identity recorded by [State.Begin].
func (s *State) Identity() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identity
}

// Phase returns the current phase.
func (s *State) Phase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// FinalHandshakeReceived reports whether the final handshake message was seen.
func (s *State) FinalHandshakeReceived() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalHandshake
}
