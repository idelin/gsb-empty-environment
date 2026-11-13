// Package netsec implements admission control for Tor v3 onion service
// descriptors submitted to a hidden-service directory (HSDir) relay.
//
// An HSDir must reject forged or rolled-back descriptors before caching them.
// The policy enforced by Admitter is:
//
//   - hs-descriptor version must be 3;
//   - the descriptor-signing-key certificate must be an Ed25519 signing-key
//     certificate (type 4) that has not expired;
//   - the certificate must be signed by the blinded key of the current
//     time period (the key is supplied by the relay at admission time);
//   - the descriptor signature must verify against the certified signing key
//     over the fixed prefix "Tor onion service descriptor sig v3" followed by
//     the signed descriptor body;
//   - the revision-counter must be strictly greater than the counter already
//     cached for that blinded key.
//
// All cryptographic verification is injected by the caller through the
// Verifier interface, so this package never imports a specific key-management
// or consensus implementation.
package netsec

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DescriptorVersion is the only accepted hs-descriptor protocol version.
const DescriptorVersion = 3

// SignaturePrefix is the fixed domain-separation string prepended to the
// signed descriptor body when verifying the descriptor signature, as defined
// by rend-spec-v3 section 2.5 ("Signing").
const SignaturePrefix = "Tor onion service descriptor sig v3"

// CertTypeSigningKey marks an Ed25519 certificate whose certified key is an
// onion-service descriptor signing key (CERT_TYPE_SIGNING_KEY == 4).
const CertTypeSigningKey CertType = 4

// CertKeyTypeEd25519 is the only certified-key type understood here
// (CERT_KEY_TYPE_ED25519 == 1).
const CertKeyTypeEd25519 CertKeyType = 1

// Policy errors returned by Admitter.Admit. Callers should use errors.Is to
// match them, as the returned errors carry contextual wrapping.
var (
	// ErrUnsupportedVersion is returned when hs-descriptor is not version 3.
	ErrUnsupportedVersion = errors.New("unsupported hs-descriptor version")

	// ErrCertificateExpired is returned when the signing certificate's
	// expiration hour is not in the future.
	ErrCertificateExpired = errors.New("descriptor signing certificate expired")

	// ErrCertificateSignature is returned when the signing certificate does
	// not verify against the current-period blinded key.
	ErrCertificateSignature = errors.New("invalid descriptor signing certificate signature")

	// ErrBadSignature is returned when the descriptor signature does not
	// verify over SignaturePrefix + SignedBody with the certified signing key.
	ErrBadSignature = errors.New("invalid descriptor signature")

	// ErrRevisionRollback is returned when revision-counter is not strictly
	// greater than the cached counter for the blinded key.
	ErrRevisionRollback = errors.New("revision-counter rollback rejected")

	// ErrMalformedDescriptor is returned for structurally invalid descriptors
	// or certificates.
	ErrMalformedDescriptor = errors.New("malformed onion service descriptor")

	// ErrInvalidBlindedKey is returned when the caller supplies a malformed
	// current-period blinded key.
	ErrInvalidBlindedKey = errors.New("invalid blinded key")
)

var errNilDescriptor = errors.New("nil descriptor")

// Verifier performs the cryptographic checks required by the admission
// policy. Implementations are supplied by the relay: the relay owns the
// current time-period blinded key and the Ed25519 verification routines.
type Verifier interface {
	// VerifyDescriptorCert verifies that cert was signed by blindedKey, the
	// blinded key of the current time period, over cert.CertSignedPart().
	VerifyDescriptorCert(cert *SigningCertificate, blindedKey ed25519.PublicKey) error

	// VerifyDescriptorSignature verifies that signature is a valid Ed25519
	// signature of signedMessage made with signingKey. The caller has already
	// prepended SignaturePrefix to the descriptor body.
	VerifyDescriptorSignature(signingKey ed25519.PublicKey, signedMessage, signature []byte) error
}

// Option customizes an Admitter.
type Option func(*Admitter)

// WithClock injects a clock, which is mainly useful for deterministic tests
// of certificate expiration. The zero value keeps time.Now.
func WithClock(now func() time.Time) Option {
	return func(a *Admitter) {
		if now != nil {
			a.now = now
		}
	}
}

// Admitter applies the HSDir descriptor admission policy and tracks the
// highest revision-counter seen per blinded key. It is safe for concurrent
// use.
type Admitter struct {
	verifier  Verifier
	now       func() time.Time
	mu        sync.Mutex
	revisions map[string]uint64
}

// NewAdmitter returns an Admitter using verifier for all cryptographic checks.
func NewAdmitter(verifier Verifier, opts ...Option) *Admitter {
	if verifier == nil {
		panic("netsec: NewAdmitter requires a non-nil Verifier")
	}
	a := &Admitter{
		verifier:  verifier,
		now:       time.Now,
		revisions: make(map[string]uint64),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Admit applies the admission policy to desc using currentBlindedKey, the
// blinded key for the relay's current time period. On success the
// descriptor's revision-counter is recorded as the new cached value.
func (a *Admitter) Admit(desc *Descriptor, currentBlindedKey ed25519.PublicKey) error {
	if desc == nil {
		return errNilDescriptor
	}
	if len(currentBlindedKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: expected %d bytes, got %d",
			ErrInvalidBlindedKey, ed25519.PublicKeySize, len(currentBlindedKey))
	}

	if desc.Version != DescriptorVersion {
		return fmt.Errorf("%w: got %d, want %d",
			ErrUnsupportedVersion, desc.Version, DescriptorVersion)
	}

	cert := desc.Certificate
	if cert == nil {
		return fmt.Errorf("%w: missing descriptor-signing-key certificate", ErrMalformedDescriptor)
	}
	if cert.Type != CertTypeSigningKey {
		return fmt.Errorf("%w: certificate type %d is not signing-key",
			ErrMalformedDescriptor, cert.Type)
	}
	if cert.KeyType != CertKeyTypeEd25519 {
		return fmt.Errorf("%w: certified key type %d is not Ed25519",
			ErrMalformedDescriptor, cert.KeyType)
	}
	if len(cert.SigningKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: certified signing key has wrong length", ErrMalformedDescriptor)
	}

	now := a.now()
	if !cert.Expiration.After(now) {
		return fmt.Errorf("%w: expiration %s is not after %s",
			ErrCertificateExpired, cert.Expiration.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}

	if err := a.verifier.VerifyDescriptorCert(cert, currentBlindedKey); err != nil {
		return fmt.Errorf("%w: %v", ErrCertificateSignature, err)
	}

	if len(desc.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has wrong length", ErrMalformedDescriptor)
	}
	signedMessage := make([]byte, 0, len(SignaturePrefix)+len(desc.SignedBody))
	signedMessage = append(signedMessage, SignaturePrefix...)
	signedMessage = append(signedMessage, desc.SignedBody...)
	if err := a.verifier.VerifyDescriptorSignature(cert.SigningKey, signedMessage, desc.Signature); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}

	cacheKey := hex.EncodeToString(currentBlindedKey)
	a.mu.Lock()
	defer a.mu.Unlock()
	if previous, ok := a.revisions[cacheKey]; ok && desc.RevisionCounter <= previous {
		return fmt.Errorf("%w: counter %d is not greater than cached %d",
			ErrRevisionRollback, desc.RevisionCounter, previous)
	}
	a.revisions[cacheKey] = desc.RevisionCounter
	return nil
}

// CachedRevision returns the highest revision-counter admitted so far for the
// given blinded key. The boolean reports whether any descriptor was admitted.
func (a *Admitter) CachedRevision(blindedKey ed25519.PublicKey) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	previous, ok := a.revisions[hex.EncodeToString(blindedKey)]
	return previous, ok
}
