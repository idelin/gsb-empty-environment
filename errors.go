package handoff

import "errors"

var (
	// ErrDigestLength indicates a non-SHA-256 digest encoding.
	ErrDigestLength = errors.New("handoff: digest must be 32 bytes")
	// ErrUnknownEgress indicates no trusted key is configured.
	ErrUnknownEgress = errors.New("handoff: unknown or untrusted egress")
	// ErrBadSignature indicates signature verification failure.
	ErrBadSignature = errors.New("handoff: invalid receipt signature")
	// ErrReplay indicates a receipt or nonce was already observed.
	ErrReplay = errors.New("handoff: replayed receipt")
	// ErrStaleGeneration indicates an old-generation acknowledgement.
	ErrStaleGeneration = errors.New("handoff: stale statement generation")
	// ErrInvalidSpec indicates an invalid handoff specification.
	ErrInvalidSpec = errors.New("handoff: invalid specification")
	// ErrUnsafePlan indicates exhaustive verification found a safety violation.
	ErrUnsafePlan = errors.New("handoff: plan is not safe under allowed orders")
	// ErrCertificateMismatch indicates certificate replay did not reproduce it.
	ErrCertificateMismatch = errors.New("handoff: certificate does not match protocol")
	// ErrTracesExceeded indicates exhaustive search hit its safety bound.
	ErrTracesExceeded = errors.New("handoff: interleaving limit exceeded")
)
