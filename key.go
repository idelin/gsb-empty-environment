package netsec

import (
	"crypto"
	"fmt"
	"strings"
	"time"
)

// Binding is one signed assertion that a user identifier belongs to a key.
// The Signature covers BindingStatement(Fingerprint, UserID) and must verify
// against the key's own public key before the binding is accepted.
type Binding struct {
	UserID      string    `json:"user_id"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Algorithm   Algorithm `json:"algorithm,omitempty"`
	Signature   []byte    `json:"signature"`
	Verified    bool      `json:"verified,omitempty"`
	Created     time.Time `json:"created,omitempty"`
}

// KeyRecord is the importable unit understood by Directory. PublicKey may be
// any standard library public key, a PKIX DER blob, a PEM block or a raw
// Ed25519 key.
//
// Fingerprint is the complete fingerprint claimed by the publisher. It is
// checked against the key material during Import; a record for Alice cannot
// simply claim Alice's fingerprint while carrying Mallory's key.
//
// ShortID is a display hint only. When empty, the last eight hexadecimal
// digits of Fingerprint are used. Supplying a hint never makes it authoritative
// and never enables a key to match a query on its own.
//
// Bindings is the preferred form. For a single binding, UserID plus
// BindingSignature is accepted as a convenience.
type KeyRecord struct {
	Fingerprint string           `json:"fingerprint"`
	ShortID     string           `json:"short_id,omitempty"`
	UserID      string           `json:"user_id,omitempty"`
	PublicKey   crypto.PublicKey `json:"public_key"`

	Bindings         []Binding `json:"bindings,omitempty"`
	BindingSignature []byte    `json:"binding_signature,omitempty"`
	BindingAlgorithm Algorithm `json:"binding_algorithm,omitempty"`

	RevocationSignature []byte    `json:"revocation_signature,omitempty"`
	RevocationAlgorithm Algorithm `json:"revocation_algorithm,omitempty"`
	RevocationUserID    string    `json:"revocation_user_id,omitempty"`
	RevocationAll       bool      `json:"revocation_all,omitempty"`
	Revoked             bool      `json:"revoked,omitempty"`

	// Verified is informational. It never replaces a signature check.
	Verified bool `json:"verified,omitempty"`
}

// Key is an alias kept for callers that model the record as a key.
type Key = KeyRecord

// NewKeyRecord computes the full fingerprint and short identifier for publicKey
// and returns an unsigned record. Call SignBinding before importing it.
func NewKeyRecord(publicKey crypto.PublicKey, userID string) (*KeyRecord, error) {
	key, _, err := normalizePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	fingerprint, err := Fingerprint(key)
	if err != nil {
		return nil, err
	}
	shortID, err := DerivedShortID(fingerprint)
	if err != nil {
		return nil, err
	}
	return &KeyRecord{
		Fingerprint: fingerprint,
		ShortID:     shortID,
		UserID:      normalizeUserID(userID),
		PublicKey:   key,
	}, nil
}

// NewBoundKey creates a record and immediately binds userID using signer.
func NewBoundKey(publicKey crypto.PublicKey, signer crypto.Signer, userID string) (*KeyRecord, error) {
	record, err := NewKeyRecord(publicKey, userID)
	if err != nil {
		return nil, err
	}
	if err := record.SignBinding(signer, userID); err != nil {
		return nil, err
	}
	if err := VerifyKeyRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

// SignBinding signs userID together with the record's full fingerprint using
// signer. The signer's public key must match the record's PublicKey.
func (r *KeyRecord) SignBinding(signer crypto.Signer, userID string) error {
	if r == nil {
		return fmt.Errorf("%w: nil record", ErrUnverifiedBinding)
	}
	if signer == nil {
		return fmt.Errorf("%w: nil signer", ErrInvalidSignature)
	}
	if r.PublicKey == nil {
		r.PublicKey = signer.Public()
	}
	if !PublicKeysEqual(r.PublicKey, signer.Public()) {
		return fmt.Errorf("%w: signer public key differs from record", ErrKeyMismatch)
	}
	if r.Fingerprint == "" {
		fingerprint, err := Fingerprint(r.PublicKey)
		if err != nil {
			return err
		}
		r.Fingerprint = fingerprint
	}
	fingerprint, err := NormalizeFingerprint(r.Fingerprint)
	if err != nil {
		return err
	}
	userID = normalizeUserID(userID)
	if userID == "" {
		return ErrUserIDRequired
	}
	signature, algorithm, err := SignMessage(signer, BindingStatement(fingerprint, userID))
	if err != nil {
		return err
	}
	r.Fingerprint = fingerprint
	if r.ShortID == "" {
		shortID, err := DerivedShortID(fingerprint)
		if err != nil {
			return err
		}
		r.ShortID = shortID
	}
	r.UserID = userID
	r.BindingSignature = signature
	r.BindingAlgorithm = algorithm
	r.Verified = true
	return nil
}

// Bind is an alias for SignBinding.
func (r *KeyRecord) Bind(signer crypto.Signer, userID string) error {
	return r.SignBinding(signer, userID)
}

// SignRevocation creates a revocation certificate for userID. An empty userID
// revokes every binding of the key. The signature is made by the key itself.
func (r *KeyRecord) SignRevocation(signer crypto.Signer, userID string) error {
	if r == nil {
		return fmt.Errorf("%w: nil record", ErrInvalidRevocation)
	}
	if signer == nil {
		return fmt.Errorf("%w: nil signer", ErrInvalidRevocation)
	}
	if r.PublicKey == nil {
		r.PublicKey = signer.Public()
	}
	if !PublicKeysEqual(r.PublicKey, signer.Public()) {
		return fmt.Errorf("%w: signer public key differs from record", ErrKeyMismatch)
	}
	if r.Fingerprint == "" {
		fingerprint, err := Fingerprint(r.PublicKey)
		if err != nil {
			return err
		}
		r.Fingerprint = fingerprint
	}
	fingerprint, err := NormalizeFingerprint(r.Fingerprint)
	if err != nil {
		return err
	}
	userID = normalizeUserID(userID)
	signature, algorithm, err := SignMessage(signer, RevocationStatement(fingerprint, userID))
	if err != nil {
		return err
	}
	r.Fingerprint = fingerprint
	r.RevocationSignature = signature
	r.RevocationAlgorithm = algorithm
	r.RevocationUserID = userID
	r.RevocationAll = userID == ""
	r.Revoked = true
	return nil
}

// VerifyBinding verifies a detached self-signature over the binding statement
// for fingerprint and userID.
func VerifyBinding(publicKey any, fingerprint, userID string, signature []byte, algorithms ...Algorithm) error {
	fingerprint, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return err
	}
	userID = normalizeUserID(userID)
	if userID == "" {
		return ErrUserIDRequired
	}
	return VerifyMessage(publicKey, BindingStatement(fingerprint, userID), signature, algorithms...)
}

// VerifyRevocation verifies a detached revocation certificate.
func VerifyRevocation(publicKey any, fingerprint, userID string, signature []byte, algorithms ...Algorithm) error {
	fingerprint, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return err
	}
	return VerifyMessage(publicKey, RevocationStatement(fingerprint, normalizeUserID(userID)), signature, algorithms...)
}

// VerifyKeyRecord validates the complete record without adding it to a
// directory. The key's fingerprint must match its key material, every binding
// must carry a valid self-signature and any revocation certificate must verify.
func VerifyKeyRecord(record *KeyRecord) error {
	if record == nil {
		return fmt.Errorf("%w: nil record", ErrInvalidPublicKey)
	}
	_, err := normalizeRecord(record)
	if err != nil {
		return err
	}
	return nil
}

// ShortIdentifier returns the effective short-identifier hint for a fingerprint.
func ShortIdentifier(fingerprint string) (string, error) {
	return DerivedShortID(fingerprint)
}

// UserIDBound reports whether record carries a valid binding for userID. It
// does not consult revocation state; use Directory.Lookup for an authoritative
// answer.
func UserIDBound(record *KeyRecord, userID string) error {
	normalized, err := normalizeRecord(record)
	if err != nil {
		return err
	}
	userID = normalizeUserID(userID)
	if userID == "" {
		return ErrUserIDRequired
	}
	if len(normalized.bindings[userID]) == 0 {
		return ErrUserMismatch
	}
	return nil
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
