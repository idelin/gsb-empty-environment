package netsec

import (
	"fmt"
	"strings"
	"sync"
)

// SessionBinding is one signed hop observed on an agent connection. The
// signature is made by the hop's host key over SessionID, proving that the
// host actually participated in the corresponding SSH key exchange.
type SessionBinding struct {
	Hostname     string
	Username     string
	HostKey      []byte
	SessionID    []byte
	Signature    []byte
	IsForwarding bool
}

// ParseSessionBind parses an SSH_AGENTC_EXTENSION request containing the
// session-bind@openssh.com extension. Both a bare request body and a complete
// length-prefixed agent packet are accepted.
func ParseSessionBind(raw []byte) (*SessionBinding, error) {
	body, err := stripAgentFrame(raw)
	if err != nil {
		return nil, err
	}
	r := newWireReader(body)
	typ, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if typ != AgentcExtension {
		return nil, fmt.Errorf("%w: message type %d is not SSH_AGENTC_EXTENSION", ErrMalformedMessage, typ)
	}
	name, err := r.readStringValue()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if name != sessionBindExtension {
		return nil, fmt.Errorf("%w: extension %q is not %q", ErrMalformedMessage, name, sessionBindExtension)
	}
	hostKey, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	sessionID, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	signature, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	isForwarding, err := r.readBool()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	if err := r.expectEnd(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}

	b := &SessionBinding{
		HostKey:      copyBytes(hostKey),
		SessionID:    copyBytes(sessionID),
		Signature:    copyBytes(signature),
		IsForwarding: isForwarding,
	}
	if err := validateBindingShape(b); err != nil {
		return nil, err
	}
	return b, nil
}

func validateBindingShape(b *SessionBinding) error {
	if b == nil {
		return fmt.Errorf("%w: nil binding", ErrMissingSessionBinding)
	}
	if err := ValidatePublicKeyBlob(b.HostKey); err != nil {
		return fmt.Errorf("%w: invalid host key: %v", ErrMalformedMessage, err)
	}
	if len(b.SessionID) == 0 {
		return fmt.Errorf("%w: empty session identifier", ErrMissingSessionBinding)
	}
	if len(b.Signature) == 0 {
		return fmt.Errorf("%w: binding has no host signature", ErrMissingSessionBinding)
	}
	return nil
}

// Session tracks the ordered, signed host bindings seen on one forwarded
// agent connection. Duplicate hosts, session identifiers, or loops are
// rejected as soon as they are presented.
type Session struct {
	mu       sync.RWMutex
	bindings []SessionBinding
}

func NewSession() *Session {
	return &Session{}
}

// Bind verifies the hop's host-key signature over its session identifier and
// then records the hop. A binding cannot be reused for a second session, and a
// host cannot occur twice in one forwarding chain.
func (s *Session) Bind(b SessionBinding) error {
	if err := validateBindingShape(&b); err != nil {
		return err
	}
	if err := verifySSHSignature(b.HostKey, b.Signature, b.SessionID); err != nil {
		return fmt.Errorf("%w: %v", ErrBindingSignature, err)
	}
	return s.recordBinding(b)
}

// AddVerifiedBinding records a binding whose session signature was verified by
// the caller in another trust domain. Structural checks (including duplicate
// and loop detection) are still enforced. Do not call this with unverified
// attacker-controlled extension payloads; use Bind or BindMessage instead.
func (s *Session) AddVerifiedBinding(b SessionBinding) error {
	if err := validateBindingShape(&b); err != nil {
		return err
	}
	return s.recordBinding(b)
}

// BindMessage parses and verifies a session-bind@openssh.com extension, then
// records the hop.
func (s *Session) BindMessage(raw []byte) error {
	b, err := ParseSessionBind(raw)
	if err != nil {
		return err
	}
	return s.Bind(*b)
}

func (s *Session) recordBinding(b SessionBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.bindings {
		if equalPublicKeys(existing.HostKey, b.HostKey) {
			return fmt.Errorf("%w: host key already appears in chain", ErrDuplicateNode)
		}
		if len(existing.SessionID) > 0 && string(existing.SessionID) == string(b.SessionID) {
			return fmt.Errorf("%w: session identifier was already bound", ErrDuplicateNode)
		}
		if existing.Hostname != "" && b.Hostname != "" && strings.EqualFold(existing.Hostname, b.Hostname) {
			return fmt.Errorf("%w: host %q already appears in chain", ErrDuplicateNode, b.Hostname)
		}
	}

	b.HostKey = copyBytes(b.HostKey)
	b.SessionID = copyBytes(b.SessionID)
	b.Signature = copyBytes(b.Signature)
	s.bindings = append(s.bindings, b)
	return nil
}

// Bindings returns a copy of the recorded chain in forwarding order.
func (s *Session) Bindings() []SessionBinding {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionBinding, len(s.bindings))
	for i := range s.bindings {
		out[i] = cloneBinding(s.bindings[i])
	}
	return out
}

// Chain returns the host-key chain recorded by the session. Usernames and
// hostnames are included when the caller supplied them.
func (s *Session) Chain() Chain {
	bindings := s.Bindings()
	chain := make(Chain, len(bindings))
	for i, b := range bindings {
		chain[i] = Hop{
			Hostname: b.Hostname,
			Username: b.Username,
			HostKey:  copyBytes(b.HostKey),
		}
	}
	return chain
}

// Reset discards all bindings, for example when a socket is reused.
func (s *Session) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.bindings = nil
	s.mu.Unlock()
}

func cloneBinding(b SessionBinding) SessionBinding {
	return SessionBinding{
		Hostname:     b.Hostname,
		Username:     b.Username,
		HostKey:      copyBytes(b.HostKey),
		SessionID:    copyBytes(b.SessionID),
		Signature:    copyBytes(b.Signature),
		IsForwarding: b.IsForwarding,
	}
}
