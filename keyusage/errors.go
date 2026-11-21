package keyusage

import "errors"

// Sentinel errors returned by the policy enforcer. Callers should use
// errors.Is to match them against wrapped, context-rich errors.
var (
	// ErrWeakAlgorithm is returned when an algorithm is explicitly classified
	// as weak (e.g. 3DES, RC4, SHA-1, RSA-1024).
	ErrWeakAlgorithm = errors.New("weak algorithm rejected")

	// ErrAlgorithmNotAllowed is returned when the algorithm is not weak but
	// is missing from the configured algorithm whitelist.
	ErrAlgorithmNotAllowed = errors.New("algorithm not in whitelist")

	// ErrPurposeNotAllowed is returned when a key's declared purposes do not
	// cover the requested operation, e.g. a signing key used for encryption.
	ErrPurposeNotAllowed = errors.New("key purpose does not permit this operation")

	// ErrWrappingKeyMisuse is returned when a key wrapping purpose is applied
	// to business data, or a wrapping-only key is asked to encrypt/decrypt
	// business data.
	ErrWrappingKeyMisuse = errors.New("wrapping key must not process business data")

	// ErrDerivationNotAllowed is returned when the parent key is not allowed
	// to derive other keys.
	ErrDerivationNotAllowed = errors.New("parent key is not permitted to derive keys")

	// ErrDerivedPurposeExceedsParent is returned when a derived key requests
	// purposes that are not a subset of its parent's purposes.
	ErrDerivedPurposeExceedsParent = errors.New("derived key purposes exceed parent purposes")

	// ErrInvalidKey is returned when a required key argument is invalid.
	ErrInvalidKey = errors.New("invalid key")

	// ErrInvalidOperation is returned when an operation does not specify
	// exactly one purpose.
	ErrInvalidOperation = errors.New("invalid operation")
)
