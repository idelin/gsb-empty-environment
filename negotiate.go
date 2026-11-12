package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Protocol identifies an application protocol version, e.g. "netsec/2".
type Protocol string

const (
	ProtocolLegacy Protocol = "netsec/1"
	ProtocolV2     Protocol = "netsec/2"
)

// FailureCategory classifies why a handshake failed. The category decides
// whether a legacy fallback is even eligible for consideration: an active
// truncation is never evidence of missing capability.
type FailureCategory string

const (
	FailureNoCommonProtocol FailureCategory = "no_common_protocol"
	FailureTruncated        FailureCategory = "truncated"
	FailureReplay           FailureCategory = "replay_detected"
	FailureTransport        FailureCategory = "transport"
)

// HandshakeError is a failure bound to the handshake instance that raised it.
type HandshakeError struct {
	InstanceID [16]byte
	Category   FailureCategory
	Err        error
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("netsec: handshake %s failed (%s): %v",
		hex.EncodeToString(e.InstanceID[:]), e.Category, e.Err)
}

func (e *HandshakeError) Unwrap() error { return e.Err }

const (
	kindClientHello = "client_hello"
	kindServerHello = "server_hello"
	kindFailure     = "failure"

	maxFrameSize = 1 << 20
)

// wireMessage carries client proposals, server selections and failure
// notices. Every message is bound to a handshake instance via InstanceID.
type wireMessage struct {
	Kind       string   `json:"kind"`
	InstanceID string   `json:"instance_id"`
	Protocols  []string `json:"protocols,omitempty"`
	Selected   string   `json:"selected,omitempty"`
	Category   string   `json:"category,omitempty"`
}

// Config configures a Negotiator.
type Config struct {
	// Protocols lists supported protocols, highest preference first.
	Protocols []Protocol
	// Legacy is the fallback protocol, used only when explicitly allowed.
	Legacy Protocol
	// AllowLegacyFallback must be explicitly set to permit falling back to
	// Legacy after an authenticated no_common_protocol failure.
	AllowLegacyFallback bool
	// Store persists the highest confirmed capability. Required, so a
	// negotiator cannot silently run without downgrade evidence.
	Store *CapabilityStore
}

// Negotiator performs instance-bound protocol negotiation for a session host.
type Negotiator struct {
	protocols     []Protocol
	legacy        Protocol
	allowFallback bool
	store         *CapabilityStore
}

func NewNegotiator(cfg Config) (*Negotiator, error) {
	if len(cfg.Protocols) == 0 {
		return nil, errors.New("netsec: no protocols configured")
	}
	if cfg.Store == nil {
		return nil, errors.New("netsec: capability store is required")
	}
	if cfg.AllowLegacyFallback && cfg.Legacy == "" {
		return nil, errors.New("netsec: fallback allowed but no legacy protocol configured")
	}
	return &Negotiator{
		protocols:     append([]Protocol(nil), cfg.Protocols...),
		legacy:        cfg.Legacy,
		allowFallback: cfg.AllowLegacyFallback,
		store:         cfg.Store,
	}, nil
}

// ClientHandshake proposes the configured protocols and returns the selected
// one. It returns the legacy protocol only when the server sent an
// instance-bound no_common_protocol failure and the fallback policy permits
// it. Truncation, replay and unbound failures are always hard errors.
func (n *Negotiator) ClientHandshake(rw io.ReadWriter) (Protocol, error) {
	id, err := newInstanceID()
	if err != nil {
		return "", err
	}
	hello := wireMessage{Kind: kindClientHello, InstanceID: encodeID(id)}
	for _, p := range n.protocols {
		hello.Protocols = append(hello.Protocols, string(p))
	}
	if err := sendMessage(rw, hello); err != nil {
		return "", &HandshakeError{id, FailureTransport, err}
	}
	msg, err := recvMessage(rw)
	if err != nil {
		return "", &HandshakeError{id, classifyRecvError(err), err}
	}
	switch msg.Kind {
	case kindServerHello:
		if msg.InstanceID != hello.InstanceID {
			return "", &HandshakeError{id, FailureReplay,
				errors.New("server hello not bound to this handshake instance")}
		}
		sel := Protocol(msg.Selected)
		if !containsProtocol(n.protocols, sel) {
			return "", &HandshakeError{id, FailureTransport,
				fmt.Errorf("server selected unoffered protocol %q", sel)}
		}
		if err := n.store.Confirm(sel); err != nil {
			return "", &HandshakeError{id, FailureTransport, err}
		}
		return sel, nil
	case kindFailure:
		if msg.InstanceID != hello.InstanceID {
			return "", &HandshakeError{id, FailureReplay,
				errors.New("failure notice not bound to this handshake instance")}
		}
		cat := FailureCategory(msg.Category)
		if cat == FailureNoCommonProtocol && n.shouldFallback() {
			return n.legacy, nil
		}
		return "", &HandshakeError{id, cat, errors.New("server rejected negotiation")}
	default:
		return "", &HandshakeError{id, FailureTransport,
			fmt.Errorf("unexpected message kind %q", msg.Kind)}
	}
}

// ServerHandshake reads a client proposal, selects the most preferred mutual
// protocol and answers with an instance-bound selection or failure notice.
func (n *Negotiator) ServerHandshake(rw io.ReadWriter) (Protocol, error) {
	msg, err := recvMessage(rw)
	if err != nil {
		return "", &HandshakeError{Category: classifyRecvError(err), Err: err}
	}
	if msg.Kind != kindClientHello {
		return "", &HandshakeError{Category: FailureTransport,
			Err: fmt.Errorf("expected client hello, got %q", msg.Kind)}
	}
	id, err := decodeID(msg.InstanceID)
	if err != nil {
		return "", &HandshakeError{Category: FailureTransport, Err: err}
	}
	offered := make([]Protocol, 0, len(msg.Protocols))
	for _, p := range msg.Protocols {
		offered = append(offered, Protocol(p))
	}
	sel, ok := selectProtocol(n.protocols, offered)
	if !ok {
		notice := wireMessage{Kind: kindFailure, InstanceID: msg.InstanceID,
			Category: string(FailureNoCommonProtocol)}
		_ = sendMessage(rw, notice)
		return "", &HandshakeError{id, FailureNoCommonProtocol,
			errors.New("no mutually supported protocol")}
	}
	reply := wireMessage{Kind: kindServerHello, InstanceID: msg.InstanceID,
		Selected: string(sel)}
	if err := sendMessage(rw, reply); err != nil {
		return "", &HandshakeError{id, FailureTransport, err}
	}
	if err := n.store.Confirm(sel); err != nil {
		return "", &HandshakeError{id, FailureTransport, err}
	}
	return sel, nil
}

// shouldFallback reports whether a legacy fallback is permitted: it requires
// explicit opt-in and the absence of any authenticated record that the peer
// confirmed a protocol newer than the legacy one.
func (n *Negotiator) shouldFallback() bool {
	if !n.allowFallback {
		return false
	}
	return !n.store.HasConfirmedAbove(n.legacy)
}

// classifyRecvError maps read failures to categories. A clean or partial EOF
// mid-handshake means the peer or a middlebox cut the stream; that is an
// active truncation, never proof of missing capability.
func classifyRecvError(err error) FailureCategory {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return FailureTruncated
	}
	return FailureTransport
}

func selectProtocol(preferred, offered []Protocol) (Protocol, bool) {
	for _, p := range preferred {
		if containsProtocol(offered, p) {
			return p, true
		}
	}
	return "", false
}

func containsProtocol(list []Protocol, p Protocol) bool {
	for _, q := range list {
		if q == p {
			return true
		}
	}
	return false
}

func newInstanceID() ([16]byte, error) {
	var id [16]byte
	_, err := rand.Read(id[:])
	return id, err
}

func encodeID(id [16]byte) string { return hex.EncodeToString(id[:]) }

func decodeID(s string) ([16]byte, error) {
	var id [16]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(id) {
		return id, errors.New("netsec: malformed handshake instance id")
	}
	copy(id[:], b)
	return id, nil
}

func sendMessage(w io.Writer, msg wireMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return writeFrame(w, payload)
}

func recvMessage(r io.Reader) (wireMessage, error) {
	var msg wireMessage
	payload, err := readFrame(r)
	if err != nil {
		return msg, err
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return msg, fmt.Errorf("netsec: malformed message: %w", err)
	}
	return msg, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameSize {
		return nil, errors.New("netsec: frame too large")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return buf, nil
}
