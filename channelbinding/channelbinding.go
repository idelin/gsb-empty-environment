// Package channelbinding negotiates and verifies SASL channel binding for a
// Go authentication host.
//
// A binding is accepted only when three parties agree on the binding type:
//
//   - the client declaration (what the client claims to use),
//   - the server policy (what the server requires/supports), and
//   - the security transport (what the connection actually provides).
//
// The negotiated binding data covers both the service identity and the
// specific transport handshake instance, so evidence cannot be replayed
// across connections. An empty binding is never treated as a successful
// typed binding: "unsupported" and "verification failed" are distinct
// internal results, and neither path falls back to an unbound session.
package channelbinding

import (
	"crypto/subtle"
	"errors"
	"fmt"
)

// materialLen is the length in bytes of derived channel-binding material.
const materialLen = 32

// Type identifies a channel binding mechanism.
type Type string

const (
	// None means that no channel binding is used. It is not a negotiable
	// binding mechanism and can never satisfy a typed binding requirement.
	None Type = ""

	// TLSExporter is the RFC 9266 TLS keying-material exporter binding.
	TLSExporter Type = "tls-exporter"

	// TLSServerEndPoint is the RFC 5929 TLS server end-point certificate
	// hash binding.
	TLSServerEndPoint Type = "tls-server-end-point"
)

// String returns the wire name of the binding type ("none" for None).
func (t Type) String() string {
	if t == None {
		return "none"
	}
	return string(t)
}

// Status is the internal result of a binding negotiation. The failure
// results are deliberately distinct so callers can tell an unsupported
// mechanism from a failed verification; neither failure is ever Unbound.
type Status int

const (
	// StatusUnbound is returned only for an explicitly binding-less
	// authentication that the policy explicitly allows.
	StatusUnbound Status = iota

	// StatusBound means a typed binding was negotiated and verified.
	StatusBound

	// StatusUnsupported means the declared type is not required/supported
	// by the server or not provided by the transport.
	StatusUnsupported

	// StatusInvalid means the types agreed but the binding data was empty,
	// malformed, replayed from another handshake, or otherwise wrong.
	StatusInvalid
)

// String returns a stable name for the negotiation status.
func (s Status) String() string {
	switch s {
	case StatusUnbound:
		return "unbound"
	case StatusBound:
		return "bound"
	case StatusUnsupported:
		return "unsupported"
	case StatusInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Sentinel errors. Failures always wrap one of these so hosts can use
// errors.Is, while the Status distinguishes the internal result class.
var (
	// ErrBindingRequired means the server expected a typed binding and the
	// client offered none (or an empty one).
	ErrBindingRequired = errors.New("channelbinding: channel binding required but none offered")

	// ErrUnsupportedBinding means the binding type is not accepted by the
	// server policy.
	ErrUnsupportedBinding = errors.New("channelbinding: unsupported channel binding type")

	// ErrTypeMismatch means client, server and transport do not agree on
	// one binding type.
	ErrTypeMismatch = errors.New("channelbinding: client, server and transport binding types mismatch")

	// ErrEmptyBinding means binding data that must be present is missing.
	ErrEmptyBinding = errors.New("channelbinding: empty channel binding data")

	// ErrServiceIdentity means the evidence is bound to another service.
	ErrServiceIdentity = errors.New("channelbinding: service identity mismatch")

	// ErrHandshakeMismatch means the evidence belongs to another transport
	// handshake instance (cross-connection reuse).
	ErrHandshakeMismatch = errors.New("channelbinding: handshake instance mismatch")

	// ErrBindingVerification means the re-derived binding material does not
	// match the presented evidence.
	ErrBindingVerification = errors.New("channelbinding: channel binding verification failed")

	// ErrNoTransport means a transport (or its exporter) is unavailable.
	ErrNoTransport = errors.New("channelbinding: transport or exporter unavailable")
)

// Exporter is the minimal keying-material export surface of a security
// transport, modelled on the TLS exporter from RFC 5705.
type Exporter interface {
	// ExportKeyingMaterial derives length bytes of binding material for
	// label, incorporating context. Implementations must return an error
	// rather than an empty slice on failure.
	ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error)

	// HandshakeKey returns material unique to a single transport handshake.
	// It lets the server detect evidence replayed from another connection.
	HandshakeKey() []byte
}

// Transport is the channel-binding view of the connection carrying the
// authentication exchange.
type Transport interface {
	// BindingType reports the single binding type this transport provides.
	BindingType() Type

	// Exporter returns the exporter bound to this connection.
	Exporter() Exporter
}

// Declaration is the client's binding statement as carried by the
// authentication protocol (e.g. the SCRAM gs2 channel-binding flag).
type Declaration struct {
	// Type is the binding type the client claims to use. None declares an
	// explicitly unbound client.
	Type Type
}

// Evidence is the binding material produced by the client and presented to
// the server. It binds both the service identity and the handshake instance.
type Evidence struct {
	// Type is the binding mechanism the material was derived for.
	Type Type

	// ServiceID is the service identity the material is bound to.
	ServiceID string

	// HandshakeKey identifies the transport handshake the material came from.
	HandshakeKey []byte

	// Material is the derived channel-binding key.
	Material []byte
}

// Policy is the server-side binding requirement.
type Policy struct {
	// Required is the single binding type the server mandates. When set,
	// unbound clients and every other type are rejected.
	Required Type

	// Supported lists the binding types the server may accept. An empty
	// slice defaults to TLSExporter and TLSServerEndPoint.
	Supported []Type

	// AllowNone permits an explicitly unbound authentication, and only has
	// effect when Required is None.
	AllowNone bool

	// ServiceID is the identity that accepted evidence must be bound to.
	ServiceID string
}

// exportLabel maps a negotiable type to its exporter label. The second
// result is false for unknown or empty types.
func exportLabel(t Type) (string, bool) {
	switch t {
	case TLSExporter:
		return "EXPORTER-Channel-Binding", true
	case TLSServerEndPoint:
		return "EXPORTER-tls-server-end-point", true
	default:
		return "", false
	}
}

func (p Policy) supportedTypes() []Type {
	if len(p.Supported) > 0 {
		return p.Supported
	}
	return []Type{TLSExporter, TLSServerEndPoint}
}

func containsType(list []Type, want Type) bool {
	for _, t := range list {
		if t == want {
			return true
		}
	}
	return false
}

// Bind derives client-side binding evidence from tr for serviceID. It
// refuses to produce evidence for an empty or unknown type and propagates
// empty exporter output as ErrEmptyBinding.
func Bind(tr Transport, serviceID string) (*Evidence, error) {
	if tr == nil {
		return nil, ErrNoTransport
	}
	typ := tr.BindingType()
	label, ok := exportLabel(typ)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedBinding, typ)
	}
	exporter := tr.Exporter()
	if exporter == nil {
		return nil, ErrNoTransport
	}
	handshakeKey := exporter.HandshakeKey()
	if len(handshakeKey) == 0 {
		return nil, ErrEmptyBinding
	}
	material, err := exporter.ExportKeyingMaterial(label, []byte(serviceID), materialLen)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBindingVerification, err)
	}
	if len(material) == 0 {
		return nil, ErrEmptyBinding
	}
	return &Evidence{
		Type:         typ,
		ServiceID:    serviceID,
		HandshakeKey: append([]byte(nil), handshakeKey...),
		Material:     append([]byte(nil), material...),
	}, nil
}

// Negotiate runs the three-party binding check for the authentication host.
// It is a package-level wrapper around Policy.Verify.
func Negotiate(p Policy, decl Declaration, evidence *Evidence, tr Transport) (Status, error) {
	return p.Verify(decl, evidence, tr)
}

// Verify checks the client declaration against the server policy and the
// transport. A typed binding is accepted only when all three parties name
// the same type and the evidence re-derives correctly. Every failure
// returns a non-nil error and never StatusUnbound.
func (p Policy) Verify(decl Declaration, evidence *Evidence, tr Transport) (Status, error) {
	if decl.Type == None {
		// Evidence smuggled alongside an unbound declaration is invalid,
		// never silently accepted as a typed binding.
		if evidence != nil && (evidence.Type != None || len(evidence.Material) > 0) {
			return StatusInvalid, ErrEmptyBinding
		}
		if p.Required != None || !p.AllowNone {
			return StatusInvalid, ErrBindingRequired
		}
		return StatusUnbound, nil
	}

	if p.Required != None && p.Required != decl.Type {
		return StatusUnsupported, fmt.Errorf("%w: server requires %s, client declared %s",
			ErrUnsupportedBinding, p.Required, decl.Type)
	}
	if !containsType(p.supportedTypes(), decl.Type) {
		return StatusUnsupported, fmt.Errorf("%w: %s", ErrUnsupportedBinding, decl.Type)
	}
	if p.Required != None && !containsType(p.supportedTypes(), p.Required) {
		return StatusUnsupported, fmt.Errorf("%w: policy requires %s which is not supported",
			ErrUnsupportedBinding, p.Required)
	}
	if tr == nil {
		return StatusUnsupported, ErrNoTransport
	}
	if got := tr.BindingType(); got != decl.Type {
		return StatusUnsupported, fmt.Errorf("%w: client %s, transport %s",
			ErrTypeMismatch, decl.Type, got)
	}

	// Types agree across all three parties; from here on every defect is a
	// verification failure (StatusInvalid), never a downgrade to unbound.
	if evidence == nil || evidence.Type == None ||
		len(evidence.Material) == 0 || len(evidence.HandshakeKey) == 0 {
		return StatusInvalid, ErrEmptyBinding
	}
	if evidence.Type != decl.Type {
		return StatusInvalid, fmt.Errorf("%w: declared %s, evidence %s",
			ErrTypeMismatch, decl.Type, evidence.Type)
	}
	if evidence.ServiceID != p.ServiceID {
		return StatusInvalid, fmt.Errorf("%w: evidence %q, policy %q",
			ErrServiceIdentity, evidence.ServiceID, p.ServiceID)
	}
	exporter := tr.Exporter()
	if exporter == nil {
		return StatusInvalid, ErrNoTransport
	}
	if subtle.ConstantTimeCompare(exporter.HandshakeKey(), evidence.HandshakeKey) != 1 {
		return StatusInvalid, ErrHandshakeMismatch
	}
	label, _ := exportLabel(decl.Type)
	want, err := exporter.ExportKeyingMaterial(label, []byte(p.ServiceID), len(evidence.Material))
	if err != nil {
		return StatusInvalid, fmt.Errorf("%w: exporter: %v", ErrBindingVerification, err)
	}
	if len(want) == 0 {
		return StatusInvalid, ErrEmptyBinding
	}
	if subtle.ConstantTimeCompare(want, evidence.Material) != 1 {
		return StatusInvalid, ErrBindingVerification
	}
	return StatusBound, nil
}
