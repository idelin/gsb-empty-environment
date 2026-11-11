// Package pinnedtls provides a TLS public-key pinning verifier that plugs into
// a standard [crypto/tls.Config] through its VerifyConnection callback.
//
// The verifier runs after the standard chain-of-trust and hostname
// verification performed by crypto/tls, and only inspects chains that the
// standard verifier already accepted. Pin rules carry a validity window, so
// rotation can be prepared in advance: the new rule (and therefore the new
// key) may be deployed before cutover, while the old rule remains valid until
// the old key is retired.
//
// Pins are hashes of the DER-encoded SubjectPublicKeyInfo (SPKI) of a
// certificate, matching the convention used by HTTP Public Key Pinning.
package pinnedtls

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
)

// HashSize is the length of a supported SPKI digest in bytes. Only SHA-256 is
// supported; it is the baseline hash for SPKI pinning and avoids the weaker
// algorithms of the original HPKP design.
const HashSize = sha256.Size

// SPKIPin identifies a certificate by the SHA-256 digest of its
// DER-encoded SubjectPublicKeyInfo.
type SPKIPin [HashSize]byte

// NewSPKIPin returns a pin from a raw 32-byte SHA-256 digest.
func NewSPKIPin(digest []byte) (SPKIPin, error) {
	if len(digest) != HashSize {
		return SPKIPin{}, fmt.Errorf("pinnedtls: expected %d-byte SHA-256 digest, got %d bytes", HashSize, len(digest))
	}
	var pin SPKIPin
	copy(pin[:], digest)
	return pin, nil
}

// DecodeSPKIPin parses a standard or URL-safe base64-encoded SHA-256 digest,
// with or without padding.
func DecodeSPKIPin(encoded string) (SPKIPin, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(encoded)
	}
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(encoded)
	}
	if err != nil {
		return SPKIPin{}, fmt.Errorf("pinnedtls: invalid base64 pin %q: %w", encoded, err)
	}
	return NewSPKIPin(raw)
}

// String renders the pin as standard base64, as used in HPKP headers.
func (p SPKIPin) String() string {
	return base64.StdEncoding.EncodeToString(p[:])
}

// SPKISum returns the SHA-256 pin of a certificate's SubjectPublicKeyInfo.
func SPKISum(cert *x509.Certificate) SPKIPin {
	return sha256.Sum256(cert.RawSubjectPublicKeyInfo)
}

// Rule is a time-bounded pin rule. A rule matches a connection when the
// evaluation time lies within [NotBefore, NotAfter) and at least one of its
// pins matches at least one certificate in one of the verified chains.
//
// PrimaryPins holds the currently expected keys. BackupPins holds pre-provisioned
// keys, typically the next key in the rotation. Both sets are treated
// identically during matching; the split exists to document intent and to let
// operators audit which keys are backups rather than active deployment keys.
// A rule must declare at least two distinct primary pins in practice: without
// a pre-published backup key, losing or rotating the sole pinned key locks the
// client out permanently.
type Rule struct {
	// Name identifies the rule, for example the hostname or service it applies
	// to. Names must be unique within a Config.
	Name string

	// NotBefore is the inclusive start of the rule's validity window.
	NotBefore Time

	// NotAfter is the exclusive end of the rule's validity window.
	NotAfter Time

	// PrimaryPins are the SPKI pins expected for the current deployment.
	PrimaryPins []SPKIPin

	// BackupPins are pre-provisioned pins kept available for failover and for
	// the next rotation step.
	BackupPins []SPKIPin
}

func (r Rule) activeAt(now Time) bool {
	return !now.Before(r.NotBefore) && now.Before(r.NotAfter)
}

// containsPin reports whether pin is part of the rule.
func (r Rule) containsPin(pin SPKIPin) bool {
	for _, candidate := range r.PrimaryPins {
		if candidate == pin {
			return true
		}
	}
	for _, candidate := range r.BackupPins {
		if candidate == pin {
			return true
		}
	}
	return false
}

// PinError records why pin verification failed.
type PinError struct {
	// ServerName is the SNI value of the connection, if known.
	ServerName string

	// RuleNames are the active rules that were evaluated.
	RuleNames []string

	// ChainHashes are the SPKI pins of every certificate in every verified
	// chain, ordered by chain then position.
	ChainHashes []SPKIPin

	// DidResume reports whether the failing handshake was a session resumption
	// rather than a full handshake.
	DidResume bool

	// Unwrapped is the sentinel describing the failure category.
	Unwrapped error
}

// Sentinel errors describing pin failure categories.
var (
	// ErrNoActiveRule means no rule in the current config is valid at the
	// evaluation time. Connections are rejected rather than allowed to
	// silently bypass pinning.
	ErrNoActiveRule = errors.New("pinnedtls: no active pin rule")

	// ErrNoMatch means every active rule was evaluated but none of the pins
	// appeared in any verified chain.
	ErrNoMatch = errors.New("pinnedtls: verified chain does not match any pinned key")

	// ErrEmptyChain means the connection state carried no verified chain.
	ErrEmptyChain = errors.New("pinnedtls: no verified certificate chain")
)

func (e *PinError) Error() string {
	if len(e.RuleNames) == 0 {
		return e.Unwrapped.Error()
	}
	return fmt.Sprintf("%s (rules: %v)", e.Unwrapped.Error(), e.RuleNames)
}

func (e *PinError) Unwrap() error { return e.Unwrapped }

// Is reports whether the error matches a sentinel by category, so that
// errors.Is(err, ErrNoMatch) works on wrapped *PinError values.
func (e *PinError) Is(target error) bool { return target == e.Unwrapped }
