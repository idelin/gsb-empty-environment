package netsec

import (
	"crypto"
	"crypto/hmac"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"hash"
)

// PRF is the IKEv2 pseudo-random function, for example HMAC-SHA-256.
//
// Implementations must not mutate the supplied key or data slices.
type PRF func(key, data []byte) []byte

// HMACPRF returns a PRF backed by HMAC and the supplied hash constructor.
func HMACPRF(newHash func() hash.Hash) PRF {
	if newHash == nil {
		return nil
	}
	return func(key, data []byte) []byte {
		mac := hmac.New(newHash, key)
		_, _ = mac.Write(data)
		return mac.Sum(nil)
	}
}

// AuthMethod identifies the authentication data carried in an AUTH payload.
type AuthMethod uint8

const (
	// AuthMethodHMAC uses a PSK-derived key to authenticate the AUTH input.
	AuthMethodHMAC AuthMethod = iota + 1
	// AuthMethodSignature uses the sender's certificate public key.
	AuthMethodSignature
)

func (m AuthMethod) String() string {
	switch m {
	case AuthMethodHMAC:
		return "HMAC"
	case AuthMethodSignature:
		return "signature"
	default:
		return fmt.Sprintf("AuthMethod(%d)", uint8(m))
	}
}

var (
	// ErrInvalidAuthParams indicates incomplete or malformed AUTH inputs.
	ErrInvalidAuthParams = errors.New("ikeauth: invalid AUTH parameters")
	// ErrReflectedNonce indicates that the peer nonce equals the local nonce.
	ErrReflectedNonce = errors.New("ikeauth: peer nonce equals own nonce")
	// ErrReflectedIdentity indicates that the peer identity equals the local identity.
	ErrReflectedIdentity = errors.New("ikeauth: peer identity equals own identity")
	// ErrIdentityMismatch indicates that an identity does not match a certificate SAN.
	ErrIdentityMismatch = errors.New("ikeauth: identity does not match certificate SAN")
	// ErrCertificateRequired indicates that responder authentication needs a certificate.
	ErrCertificateRequired = errors.New("ikeauth: peer certificate is required")
	// ErrMalformedIdentity indicates an invalid IKEv2 ID payload body.
	ErrMalformedIdentity = errors.New("ikeauth: malformed identity payload body")
	// ErrUnsupportedIdentity indicates an identity type that cannot be matched to a SAN.
	ErrUnsupportedIdentity = errors.New("ikeauth: unsupported identity type")
	// ErrAuthMismatch indicates that AUTH data did not authenticate the input.
	ErrAuthMismatch = errors.New("ikeauth: AUTH verification failed")
	// ErrEmptyAuth indicates that an empty AUTH payload was supplied.
	ErrEmptyAuth = errors.New("ikeauth: empty AUTH payload")
	// ErrUnsupportedSignature indicates an unsupported public key or signature hash.
	ErrUnsupportedSignature = errors.New("ikeauth: unsupported signature algorithm")
)

// Backwards-friendly aliases for callers that describe the checks as reuse
// rather than reflection.
var (
	ErrOwnNonceReused            = ErrReflectedNonce
	ErrOwnIdentityReused         = ErrReflectedIdentity
	ErrResponderIdentityMismatch = ErrIdentityMismatch
)

// AuthParams contains the exact data covered by one IKE_AUTH exchange and the
// local state needed to detect reflected messages.
//
// PeerNonce and PeerIdentity belong to the sender of the AUTH payload being
// checked (or built). OwnNonce and OwnIdentity belong to the local party.
// PeerIdentity and OwnIdentity are complete IKEv2 ID payload bodies, i.e.
// IDType (1 octet), three reserved octets, and the identification data.
//
// The AUTH input is:
//
//	RealMessage1 || PeerNonce || PRF(SKp, PeerIdentity)
//
// RealMessage1 must be the exact byte sequence of the initiator's first
// IKE_SA_INIT message. No re-encoding or normalization is performed.
type AuthParams struct {
	RealMessage1 []byte
	PeerNonce    []byte
	OwnNonce     []byte
	PeerIdentity []byte
	OwnIdentity  []byte
	SKp          []byte
	PRF          PRF

	// PeerCertificate is the certificate belonging to the AUTH sender. It is
	// mandatory when PeerIsResponder is true, because the responder's ID
	// payload must match a subjectAltName in that certificate.
	PeerCertificate *x509.Certificate

	// PeerIsResponder requests responder identity checks, including the
	// mandatory certificate SAN match.
	PeerIsResponder bool

	// SignatureHash optionally pins the hash used for a signature AUTH. A
	// zero value lets the verifier select among the supported hashes.
	SignatureHash crypto.Hash
}

// BuildAuthInput constructs the exact octet string covered by the AUTH payload:
// RealMessage1 || peerNonce || PRF(SKp, peerIdentity).
func BuildAuthInput(realMessage1, peerNonce, skp, peerIdentity []byte, prf PRF) ([]byte, error) {
	if len(realMessage1) == 0 {
		return nil, fmt.Errorf("%w: empty RealMessage1", ErrInvalidAuthParams)
	}
	if len(peerNonce) == 0 {
		return nil, fmt.Errorf("%w: empty peer nonce", ErrInvalidAuthParams)
	}
	if len(skp) == 0 {
		return nil, fmt.Errorf("%w: empty SKp", ErrInvalidAuthParams)
	}
	if len(peerIdentity) == 0 {
		return nil, fmt.Errorf("%w: empty peer identity", ErrInvalidAuthParams)
	}
	if prf == nil {
		return nil, fmt.Errorf("%w: nil PRF", ErrInvalidAuthParams)
	}

	identityKey := prf(skp, peerIdentity)
	if identityKey == nil {
		return nil, fmt.Errorf("%w: PRF returned nil", ErrInvalidAuthParams)
	}

	out := make([]byte, 0, len(realMessage1)+len(peerNonce)+len(identityKey))
	out = append(out, realMessage1...)
	out = append(out, peerNonce...)
	out = append(out, identityKey...)
	return out, nil
}

// ComputeAuthInput is an alias for BuildAuthInput.
func ComputeAuthInput(realMessage1, peerNonce, skp, peerIdentity []byte, prf PRF) ([]byte, error) {
	return BuildAuthInput(realMessage1, peerNonce, skp, peerIdentity, prf)
}

// AuthInput is an alias for BuildAuthInput.
func AuthInput(realMessage1, peerNonce, skp, peerIdentity []byte, prf PRF) ([]byte, error) {
	return BuildAuthInput(realMessage1, peerNonce, skp, peerIdentity, prf)
}

// AuthInput returns the authenticated octet string for p.PeerIdentity.
func (p AuthParams) AuthInput() ([]byte, error) {
	return BuildAuthInput(p.RealMessage1, p.PeerNonce, p.SKp, p.PeerIdentity, p.PRF)
}

// Validate performs the non-cryptographic anti-reflection and responder SAN
// checks. It is also run by every verification method.
func (p AuthParams) Validate() error {
	if err := p.validateShape(); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(p.PeerNonce, p.OwnNonce) == 1 {
		return ErrReflectedNonce
	}
	if identitiesEqual(p.PeerIdentity, p.OwnIdentity) {
		return ErrReflectedIdentity
	}
	if p.PeerIsResponder {
		if p.PeerCertificate == nil {
			return ErrCertificateRequired
		}
		if err := MatchIdentityToCertificate(p.PeerIdentity, p.PeerCertificate); err != nil {
			return err
		}
	}
	return nil
}

func (p AuthParams) validateShape() error {
	if len(p.RealMessage1) == 0 {
		return fmt.Errorf("%w: empty RealMessage1", ErrInvalidAuthParams)
	}
	if len(p.PeerNonce) == 0 {
		return fmt.Errorf("%w: empty peer nonce", ErrInvalidAuthParams)
	}
	if len(p.OwnNonce) == 0 {
		return fmt.Errorf("%w: empty own nonce", ErrInvalidAuthParams)
	}
	if len(p.PeerIdentity) == 0 {
		return fmt.Errorf("%w: empty peer identity", ErrInvalidAuthParams)
	}
	if len(p.OwnIdentity) == 0 {
		return fmt.Errorf("%w: empty own identity", ErrInvalidAuthParams)
	}
	if len(p.SKp) == 0 {
		return fmt.Errorf("%w: empty SKp", ErrInvalidAuthParams)
	}
	if p.PRF == nil {
		return fmt.Errorf("%w: nil PRF", ErrInvalidAuthParams)
	}
	return nil
}

// Verify validates the authentication data from an AUTH payload using the
// configured method. auth is the signature or MAC itself, with the leading
// AUTH Method octet already removed; use VerifyAuthPayload for a complete
// IKEv2 AUTH payload body.
//
// authKey is used only for AuthMethodHMAC. If it is empty, SKp is used as the
// HMAC key. For AuthMethodSignature, authKey is ignored.
func (p AuthParams) Verify(method AuthMethod, authKey, auth []byte) error {
	if err := p.Validate(); err != nil {
		return err
	}
	input, err := p.AuthInput()
	if err != nil {
		return err
	}

	switch method {
	case AuthMethodHMAC:
		return p.verifyHMAC(input, authKey, auth)
	case AuthMethodSignature:
		return p.verifySignature(input, auth)
	default:
		return fmt.Errorf("%w: unknown authentication method %s", ErrInvalidAuthParams, method)
	}
}

// VerifyHMAC validates an HMAC-based AUTH payload.
func (p AuthParams) VerifyHMAC(authKey, auth []byte) error {
	return p.Verify(AuthMethodHMAC, authKey, auth)
}

// VerifySignature validates a public-key signature AUTH payload against
// PeerCertificate.
func (p AuthParams) VerifySignature(auth []byte) error {
	return p.Verify(AuthMethodSignature, nil, auth)
}

// VerifyAuthPayload validates a complete IKEv2 AUTH payload body:
// AuthMethod (1 octet) || authentication data.
func (p AuthParams) VerifyAuthPayload(method AuthMethod, authKey, payload []byte) error {
	if len(payload) < 2 {
		return ErrEmptyAuth
	}
	return p.Verify(method, authKey, payload[1:])
}

// VerifyAuth is a convenience wrapper for AuthParams.Verify.
func VerifyAuth(p AuthParams, method AuthMethod, authKey, auth []byte) error {
	return p.Verify(method, authKey, auth)
}

func (p AuthParams) verifyHMAC(input, authKey, auth []byte) error {
	if len(auth) == 0 {
		return ErrEmptyAuth
	}
	if len(authKey) == 0 {
		authKey = p.SKp
	}
	if len(authKey) == 0 {
		return fmt.Errorf("%w: empty HMAC key", ErrInvalidAuthParams)
	}

	expected := p.PRF(authKey, input)
	if expected == nil || !hmac.Equal(expected, auth) {
		return ErrAuthMismatch
	}
	return nil
}

func (p AuthParams) verifySignature(input, auth []byte) error {
	if len(auth) == 0 {
		return ErrEmptyAuth
	}
	if p.PeerCertificate == nil {
		return ErrCertificateRequired
	}
	if err := VerifySignature(p.PeerCertificate, input, auth, p.SignatureHash); err != nil {
		return fmt.Errorf("%w: %v", ErrAuthMismatch, err)
	}
	return nil
}

// VerifyResponderAuth verifies a responder's second-round AUTH payload. It
// forces the responder certificate/SAN checks.
func VerifyResponderAuth(p AuthParams, method AuthMethod, authKey, auth []byte) error {
	p.PeerIsResponder = true
	return p.Verify(method, authKey, auth)
}

// VerifyInitiatorAuth verifies an initiator's second-round AUTH payload.
func VerifyInitiatorAuth(p AuthParams, method AuthMethod, authKey, auth []byte) error {
	p.PeerIsResponder = false
	return p.Verify(method, authKey, auth)
}
