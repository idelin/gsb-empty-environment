package netsec

import "errors"

var (
	// ErrMalformedToken is returned when the COSE_Sign1 wrapper, its payload,
	// or the CBOR claims cannot be decoded unambiguously.
	ErrMalformedToken = errors.New("malformed CCA attestation token")

	// ErrInvalidArgument is returned for an unusable API argument.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrMissingClaim is returned when a claim required for binding is absent.
	ErrMissingClaim = errors.New("required claim is missing")

	// ErrInvalidClaim is returned when a claim has an unexpected type or value.
	ErrInvalidClaim = errors.New("claim has invalid type or value")

	// ErrChallengeMismatch is returned when either the realm challenge does not
	// match the caller nonce or the platform challenge does not match the hash
	// of the realm public key.
	ErrChallengeMismatch = errors.New("attestation challenge mismatch")

	// ErrAlgorithmNotAllowed is returned when a hash algorithm is unknown,
	// unsupported, or outside the configured allow-list.
	ErrAlgorithmNotAllowed = errors.New("hash algorithm is not allowed")

	// ErrMeasurementLength is returned when a measurement is not the digest
	// size required by the token's hash algorithm.
	ErrMeasurementLength = errors.New("measurement length does not match hash algorithm")
)

// Aliases kept for callers that use alternate terminology.
var (
	ErrTokenFormat               = ErrMalformedToken
	ErrInvalidToken              = ErrMalformedToken
	ErrNonceMismatch             = ErrChallengeMismatch
	ErrRealmChallengeMismatch    = ErrChallengeMismatch
	ErrPlatformChallengeMismatch = ErrChallengeMismatch
	ErrHashAlgorithmNotAllowed   = ErrAlgorithmNotAllowed
	ErrHashAlgNotAllowed         = ErrAlgorithmNotAllowed
	ErrUnsupportedHashAlgorithm  = ErrAlgorithmNotAllowed
	ErrMeasurementLengthMismatch = ErrMeasurementLength
)
