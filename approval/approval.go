// Package approval implements multi-signature threshold approval for device
// update requests on the receiving service.
//
// The caller supplies an already-parsed UpdateRequest and a trusted KeySet of
// public keys. Verify authenticates each signature using standard-library
// algorithms only (Ed25519, ECDSA P-256/P-384 and RSASSA-PSS), counts distinct
// public-key materials rather than aliases or repeated signature entries, and
// enforces separate administrator and device thresholds plus the request
// deadline. Unknown algorithms are rejected and no custom cryptography is
// provided.
package approval

import (
	"crypto/x509"
	"errors"
	"time"
)

// Role identifies the authorizing role of a trusted public key.
//
// Roles are derived exclusively from the receiver's trusted KeySet. A role
// claimed inside a parsed Signature is never trusted: it cannot promote a key
// or move its vote between the two threshold buckets.
type Role string

const (
	// RoleAdmin identifies administrator signatures.
	RoleAdmin Role = "admin"
	// RoleDevice identifies device-owner signatures.
	RoleDevice Role = "device"
)

// Algorithm selects the signature scheme used to authenticate an update
// request. Only the schemes backed by the Go standard library are accepted;
// any other value is rejected as an unknown algorithm.
type Algorithm string

const (
	// AlgEd25519 uses crypto/ed25519 (pure Ed25519, no prehash).
	AlgEd25519 Algorithm = "ED25519"
	// AlgES256 uses ECDSA over P-256 with SHA-256.
	AlgES256 Algorithm = "ES256"
	// AlgES384 uses ECDSA over P-384 with SHA-384.
	AlgES384 Algorithm = "ES384"
	// AlgPS256 uses RSASSA-PSS with SHA-256.
	AlgPS256 Algorithm = "PS256"
	// AlgPS384 uses RSASSA-PSS with SHA-384.
	AlgPS384 Algorithm = "PS384"
)

// Known reports whether a is one of the accepted standard-library schemes.
func (a Algorithm) Known() bool {
	switch a {
	case AlgEd25519, AlgES256, AlgES384, AlgPS256, AlgPS384:
		return true
	}
	return false
}

var (
	// ErrUnknownAlgorithm is returned when a signature names an algorithm the
	// receiver does not implement, or when a key/algorithm pairing is invalid.
	ErrUnknownAlgorithm = errors.New("approval: unknown or unsupported signature algorithm")
	// ErrUnknownKey is returned when a signature references an alias that is
	// not present in the trusted KeySet.
	ErrUnknownKey = errors.New("approval: signature references unknown key alias")
	// ErrInvalidKey is returned when a trusted entry carries an unusable key.
	ErrInvalidKey = errors.New("approval: invalid trusted public key")
	// ErrBadSignature is returned when a signature fails cryptographic
	// verification against the covered request content.
	ErrBadSignature = errors.New("approval: signature verification failed")
	// ErrInvalidRequest is returned when a parsed request is missing required
	// content or is otherwise malformed.
	ErrInvalidRequest = errors.New("approval: malformed update request")
	// ErrExpired is returned when verification is performed after the request
	// deadline.
	ErrExpired = errors.New("approval: update request deadline has expired")
	// ErrPolicy is returned when the configured threshold policy is invalid.
	ErrPolicy = errors.New("approval: invalid threshold policy")
	// ErrUnauthorized is returned when the valid, deduplicated signatures do
	// not meet the required per-role thresholds.
	ErrUnauthorized = errors.New("approval: signature thresholds not satisfied")
)

// UpdateRequest is the already-parsed message presented by the caller. The
// approval package never parses wire formats itself; it only authenticates the
// fields listed here and enforces the policy over their signatures.
//
// A signature covers exactly these four fields: Version, DeviceGroup,
// OperationSummary and Deadline.
type UpdateRequest struct {
	// Version is the request/format version, e.g. "1".
	Version string
	// DeviceGroup names the fleet group the update applies to.
	DeviceGroup string
	// OperationSummary is a stable description of the requested operation.
	OperationSummary string
	// Deadline is the latest time at which the request may be approved.
	Deadline time.Time
	// Signatures are the parsed signatures over the four fields above.
	Signatures []Signature
}

// Signature is a single parsed signature over an UpdateRequest.
//
// Alias names the signer's entry in the trusted KeySet. ClaimedRole is
// informational only: it is ignored during verification because authority is
// derived solely from the trusted key's configured Role.
type Signature struct {
	Alias       string
	Algorithm   Algorithm
	Value       []byte
	ClaimedRole Role
}

// TrustedKey binds a public key to its alias, role and expected algorithm.
type TrustedKey struct {
	Alias     string
	Role      Role
	Algorithm Algorithm
	PublicKey any // *ed25519.PublicKey, *ecdsa.PublicKey or *rsa.PublicKey
}

// KeySet is the receiver's trusted collection of public keys.
//
// The same key material may be registered under more than one alias: that
// does not grant extra voting power because Verify counts distinct public-key
// materials, not aliases or signature entries.
type KeySet struct {
	byAlias    map[string]TrustedKey
	canonical  map[string]string // canonical key material -> first alias
	keyToRole  map[string]Role
	aliasOrder []string
}

// NewKeySet builds a validated KeySet from the caller's trusted key entries.
//
// Duplicate aliases, unknown algorithms, unsupported key types and the same
// key material being assigned conflicting roles are all rejected so that the
// trust policy cannot be ambiguous.
func NewKeySet(entries []TrustedKey) (*KeySet, error) {
	ks := &KeySet{
		byAlias:   make(map[string]TrustedKey, len(entries)),
		canonical: make(map[string]string, len(entries)),
		keyToRole: make(map[string]Role, len(entries)),
	}
	for _, entry := range entries {
		if entry.Alias == "" {
			return nil, errors.New("approval: trusted key has empty alias")
		}
		if entry.Role != RoleAdmin && entry.Role != RoleDevice {
			return nil, errors.New("approval: trusted key has unknown role")
		}
		if !entry.Algorithm.Known() {
			return nil, ErrUnknownAlgorithm
		}
		if err := checkKey(entry.PublicKey, entry.Algorithm); err != nil {
			return nil, err
		}
		if _, exists := ks.byAlias[entry.Alias]; exists {
			return nil, errors.New("approval: duplicate trusted key alias: " + entry.Alias)
		}
		material, err := canonicalKey(entry.PublicKey)
		if err != nil {
			return nil, err
		}
		if priorAlias, ok := ks.canonical[material]; ok {
			if ks.keyToRole[material] != entry.Role {
				return nil, errors.New("approval: key material registered with conflicting roles under aliases " +
					priorAlias + " and " + entry.Alias)
			}
		} else {
			ks.canonical[material] = entry.Alias
			ks.keyToRole[material] = entry.Role
		}
		ks.byAlias[entry.Alias] = entry
		ks.aliasOrder = append(ks.aliasOrder, entry.Alias)
	}
	return ks, nil
}

// Lookup returns the trusted entry registered under alias and whether it exists.
func (ks *KeySet) Lookup(alias string) (TrustedKey, bool) {
	entry, ok := ks.byAlias[alias]
	return entry, ok
}

// canonicalKey renders the DER encoding of a standard public key. DER is a
// deterministic encoding, so identical key materials always produce identical
// byte strings regardless of which alias presented them. This is purely a
// data-encoding operation (x509), not a custom cryptographic primitive.
func canonicalKey(pub any) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", ErrInvalidKey
	}
	return string(der), nil
}
