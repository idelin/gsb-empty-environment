// Package netsec implements OpenPGP signing-subkey ownership verification.
//
// A signing-capable subkey must back-sign its primary key (an embedded
// 0x19 primary-key-binding signature inside the 0x18 subkey-binding
// signature). Without that back-signature, an attacker could bind a
// victim's public signing subkey to the attacker's own primary key and
// claim the victim's signatures as their own. This package verifies the
// binding chain so a keyring can decide which primary key a message
// signature should be attributed to.
package netsec

import (
	"bytes"
	"errors"
	"time"
)

// OpenPGP signature types relevant to subkey ownership.
const (
	SigTypeSubkeyBinding     = 0x18
	SigTypePrimaryKeyBinding = 0x19
	SigTypeSubkeyRevocation  = 0x28
)

// Key flags carried by the subkey-binding signature.
const (
	KeyFlagAuthenticate = 0x01
	KeyFlagSign         = 0x02
	KeyFlagEncryptComms = 0x04
	KeyFlagEncryptStore = 0x08
)

// Verification failures returned by this package.
var (
	ErrMalformedBinding            = errors.New("netsec: incomplete subkey binding")
	ErrBindingIssuerMismatch       = errors.New("netsec: binding signature not issued by primary key")
	ErrBindingNotYetValid          = errors.New("netsec: binding signature created after verification time")
	ErrBindingVerify               = errors.New("netsec: binding signature does not verify under primary key")
	ErrBackSignatureMissing        = errors.New("netsec: signing-capable subkey lacks embedded primary key binding signature")
	ErrBackSignatureIssuerMismatch = errors.New("netsec: back-signature not issued by subkey")
	ErrBackSignatureVerify         = errors.New("netsec: back-signature does not verify under subkey")
	ErrRevocationIssuerMismatch    = errors.New("netsec: revocation not issued by primary key")
	ErrRevocationVerify            = errors.New("netsec: revocation signature does not verify under primary key")
	ErrSubkeyExpired               = errors.New("netsec: subkey expired before message signature was created")
	ErrBindingExpired              = errors.New("netsec: binding signature expired before message signature was created")
	ErrSubkeyRevoked               = errors.New("netsec: subkey revoked before message signature was created")
	ErrNoMatchingBinding           = errors.New("netsec: no binding matches the message signature issuer")
)

// PublicKey holds the public key material of a primary key or subkey.
type PublicKey struct {
	// Fingerprint uniquely identifies the key (e.g. the 20-byte v4
	// fingerprint).
	Fingerprint []byte
	// Body is the serialized public key packet body, fed into the
	// signature hash input.
	Body []byte
}

// KeyHashInput builds the hash input used by binding and revocation
// signatures: each key contributes 0x99 || two-octet length || body,
// primary key first, then subkey.
func KeyHashInput(keys ...*PublicKey) []byte {
	var out []byte
	for _, k := range keys {
		out = append(out, 0x99, byte(len(k.Body)>>8), byte(len(k.Body)))
		out = append(out, k.Body...)
	}
	return out
}

// VerifyFunc is the caller-supplied signature verification callback. It
// reports whether sig is a valid signature over hashInput made by key.
type VerifyFunc func(key *PublicKey, hashInput, sig []byte) bool

// BindingSignature is a type 0x18 subkey-binding signature made by the
// primary key over primary||subkey.
type BindingSignature struct {
	CreationTime      time.Time
	ExpirationTime    *time.Time // optional signature expiration
	KeyFlags          byte
	IssuerFingerprint []byte
	Signature         []byte
	// Embedded optionally carries the type 0x19 primary key binding
	// (back-)signature made by the subkey.
	Embedded *EmbeddedSignature
}

// EmbeddedSignature is a type 0x19 primary key binding signature made by
// the subkey over primary||subkey, embedded in the binding signature.
type EmbeddedSignature struct {
	CreationTime      time.Time
	IssuerFingerprint []byte
	Signature         []byte
}

// RevocationSignature is a type 0x28 subkey revocation signature made by
// the primary key over primary||subkey.
type RevocationSignature struct {
	CreationTime      time.Time
	IssuerFingerprint []byte
	Signature         []byte
}

// SubkeyBinding ties a subkey to a primary key through its binding
// signature and optional revocation.
type SubkeyBinding struct {
	Primary          *PublicKey
	Subkey           *PublicKey
	SubkeyExpiration *time.Time // optional key expiration time
	Binding          *BindingSignature
	Revocation       *RevocationSignature // optional type 0x28 revocation
}

// requiresBackSignature reports whether the key flags demand an embedded
// 0x19 back-signature: signing (0x02) or authentication (0x01) capable
// subkeys must back-sign; encryption-only (0x04, 0x08) subkeys need not.
func requiresBackSignature(flags byte) bool {
	return flags&(KeyFlagSign|KeyFlagAuthenticate) != 0
}

// VerifyBinding validates that b.Subkey is bound to b.Primary at time
// now: the 0x18 binding signature must be issued and signed by the
// primary key and not created in the future; signing-capable subkeys
// must carry a valid embedded 0x19 back-signature; any 0x28 revocation
// must itself be authentic.
func VerifyBinding(b *SubkeyBinding, verify VerifyFunc, now time.Time) error {
	if b == nil || b.Primary == nil || b.Subkey == nil || b.Binding == nil || verify == nil {
		return ErrMalformedBinding
	}
	bs := b.Binding
	if !bytes.Equal(bs.IssuerFingerprint, b.Primary.Fingerprint) {
		return ErrBindingIssuerMismatch
	}
	if bs.CreationTime.After(now) {
		return ErrBindingNotYetValid
	}
	hashInput := KeyHashInput(b.Primary, b.Subkey)
	if !verify(b.Primary, hashInput, bs.Signature) {
		return ErrBindingVerify
	}
	if requiresBackSignature(bs.KeyFlags) {
		emb := bs.Embedded
		if emb == nil {
			return ErrBackSignatureMissing
		}
		if !bytes.Equal(emb.IssuerFingerprint, b.Subkey.Fingerprint) {
			return ErrBackSignatureIssuerMismatch
		}
		if !verify(b.Subkey, hashInput, emb.Signature) {
			return ErrBackSignatureVerify
		}
	}
	if rev := b.Revocation; rev != nil {
		if !bytes.Equal(rev.IssuerFingerprint, b.Primary.Fingerprint) {
			return ErrRevocationIssuerMismatch
		}
		if !verify(b.Primary, hashInput, rev.Signature) {
			return ErrRevocationVerify
		}
	}
	return nil
}

// AcceptsMessageSignature reports whether a message signature created at
// sigTime may be accepted under this binding. Signatures created after
// the subkey or binding signature expired, or at/after a 0x28
// revocation, are rejected; earlier ones remain acceptable.
func (b *SubkeyBinding) AcceptsMessageSignature(sigTime time.Time) error {
	if b == nil || b.Binding == nil {
		return ErrMalformedBinding
	}
	if b.Binding.CreationTime.After(sigTime) {
		return ErrBindingNotYetValid
	}
	if exp := b.SubkeyExpiration; exp != nil && sigTime.After(*exp) {
		return ErrSubkeyExpired
	}
	if exp := b.Binding.ExpirationTime; exp != nil && sigTime.After(*exp) {
		return ErrBindingExpired
	}
	if rev := b.Revocation; rev != nil && !sigTime.Before(rev.CreationTime) {
		return ErrSubkeyRevoked
	}
	return nil
}

// SelectBinding resolves ownership when the same subkey is bound by
// multiple primary keys. It returns the binding whose primary key
// fingerprint matches the message signature's issuer fingerprint and
// whose binding chain (including the back-signature) verifies at now.
func SelectBinding(bindings []*SubkeyBinding, issuerFingerprint []byte, verify VerifyFunc, now time.Time) (*SubkeyBinding, error) {
	for _, b := range bindings {
		if b == nil || b.Primary == nil || b.Binding == nil {
			continue
		}
		if !bytes.Equal(b.Binding.IssuerFingerprint, issuerFingerprint) {
			continue
		}
		if !bytes.Equal(b.Primary.Fingerprint, issuerFingerprint) {
			continue
		}
		if err := VerifyBinding(b, verify, now); err != nil {
			continue
		}
		return b, nil
	}
	return nil, ErrNoMatchingBinding
}
