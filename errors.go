package netsec

import "errors"

var (
	// ErrInvalidKey is returned when the public modulus or exponent is not
	// suitable for this prefix game.
	ErrInvalidKey = errors.New("netsec: invalid RSA public key")
	// ErrInvalidCiphertext is returned when c is not a canonical residue.
	ErrInvalidCiphertext = errors.New("netsec: invalid target ciphertext")
	// ErrInvalidOracle is returned when no usable callback was supplied.
	ErrInvalidOracle = errors.New("netsec: invalid oracle callback")
	// ErrOracleUnavailable may be returned by a callback to signal a temporary
	// service failure.  It is treated like FeedbackUnavailable.
	ErrOracleUnavailable = errors.New("netsec: oracle temporarily unavailable")
	// ErrInvalidFeedback is returned when a callback uses an unknown answer.
	ErrInvalidFeedback = errors.New("netsec: invalid oracle feedback")
	// ErrInconsistent reports feedback that contradicts the candidate set or
	// the public key/ciphertext binding.  A forged callback cannot be reported
	// as a successful recovery.
	ErrInconsistent = errors.New("netsec: inconsistent oracle feedback")
	// ErrCheckpointMismatch reports a checkpoint with a broken binding, a
	// different key/ciphertext, or a query history that does not reproduce the
	// stored candidate state.
	ErrCheckpointMismatch = errors.New("netsec: recovery checkpoint mismatch")
	// ErrNoMultiplier is returned if no further valid multiplier can be found.
	ErrNoMultiplier = errors.New("netsec: no valid multiplier found")
	// ErrInvalidInterval is returned for a malformed candidate interval.
	ErrInvalidInterval = errors.New("netsec: invalid interval")
)

// Descriptive aliases for callers that prefer longer names.
var (
	ErrOracleInconsistent = ErrInconsistent
	ErrCheckpointBinding  = ErrCheckpointMismatch
	ErrUnavailable        = ErrOracleUnavailable
	ErrEmptyIntersection  = ErrInconsistent
	ErrContradiction      = ErrInconsistent
)
