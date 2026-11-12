package netsec

import "errors"

var (
	// ErrInvalidKey reports a malformed or out-of-policy package key.
	ErrInvalidKey = errors.New("netsec: invalid package key")

	// ErrEmptyChecksum reports a missing artifact checksum.
	ErrEmptyChecksum = errors.New("netsec: empty checksum")

	// ErrChecksumMismatch reports different artifact bytes for an already
	// recorded version. It is never bypassable by trusting a signer change.
	ErrChecksumMismatch = errors.New("netsec: checksum mismatch")

	// ErrSignerChanged reports a new signer for a package whose previous
	// signer is still trusted. Callers must explicitly trust the change.
	ErrSignerChanged = errors.New("netsec: signer changed")

	// ErrUnsignedDowngrade reports an unsigned artifact for a package that
	// already has a signing identity. This is never allowed.
	ErrUnsignedDowngrade = errors.New("netsec: signed package downgraded to unsigned")
)
