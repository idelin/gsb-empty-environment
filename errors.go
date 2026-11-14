package netsec

import "errors"

// Sentinel errors returned by the identity binding component. Callers should
// use errors.Is to test for them because the component adds context where it
// is useful.
var (
	// ErrInvalidFingerprint means the value is not a complete hexadecimal
	// fingerprint (40 or 64 hexadecimal digits).
	ErrInvalidFingerprint = errors.New("netsec: invalid full fingerprint")
	// ErrFingerprintRequired means the caller only supplied a short
	// identifier or a user identifier. Neither is sufficient to resolve a key.
	ErrFingerprintRequired = errors.New("netsec: full fingerprint required")
	// ErrFingerprintMismatch means the claimed fingerprint was not derived
	// from the supplied public key.
	ErrFingerprintMismatch = errors.New("netsec: fingerprint does not match public key")
	// ErrFingerprintCollision means two different public keys were published
	// under the same full fingerprint.
	ErrFingerprintCollision = errors.New("netsec: full fingerprint collision")
	// ErrUserIDRequired means the caller did not supply a user identifier.
	ErrUserIDRequired = errors.New("netsec: user identifier required")
	// ErrUserMismatch means the fingerprint resolved but the requested user
	// identifier is not bound to that key.
	ErrUserMismatch = errors.New("netsec: user identifier is not bound to key")
	// ErrShortIDMismatch means a supplied hint does not agree with the
	// resolved key.
	ErrShortIDMismatch = errors.New("netsec: short identifier hint does not match")
	// ErrShortIDCollision means a short identifier resolves to more than one
	// key. It is never safe to treat that as an unambiguous answer.
	ErrShortIDCollision = errors.New("netsec: short identifier collision")
	// ErrMultipleResults means more than one key matched the query.
	ErrMultipleResults = errors.New("netsec: multiple keys matched")
	// ErrUnverifiedBinding means the binding has no valid self-signature.
	ErrUnverifiedBinding = errors.New("netsec: unverified identity binding")
	// ErrInvalidSignature means a detached signature did not verify.
	ErrInvalidSignature = errors.New("netsec: invalid signature")
	// ErrInvalidRevocation means the revocation certificate is malformed or
	// not signed by the key it revokes.
	ErrInvalidRevocation = errors.New("netsec: invalid revocation certificate")
	// ErrRevoked means the key or binding has been revoked.
	ErrRevoked = errors.New("netsec: key is revoked")
	// ErrNotFound means no matching key was found.
	ErrNotFound = errors.New("netsec: key not found")
	// ErrInvalidPublicKey means the value cannot be used as a public key.
	ErrInvalidPublicKey = errors.New("netsec: invalid public key")
	// ErrKeyMismatch means the signing key does not match the public key in
	// the record.
	ErrKeyMismatch = errors.New("netsec: signing key does not match record")
)

// Compatibility aliases for callers that use the shorter security terms.
// They compare identically to the canonical errors via errors.Is.
var (
	ErrAmbiguous       = ErrMultipleResults
	ErrKeyNotFound     = ErrNotFound
	ErrCollision       = ErrShortIDCollision
	ErrUnverified      = ErrUnverifiedBinding
	ErrSubstitution    = ErrFingerprintMismatch
	ErrInvalidBinding  = ErrUnverifiedBinding
	ErrRevocationCheck = ErrInvalidRevocation
)
