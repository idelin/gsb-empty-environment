package netsec

import "errors"

var (
	// ErrInvalidPhase is returned when no usable exporter phase was supplied.
	ErrInvalidPhase = errors.New("netsec: invalid exporter phase")

	// ErrPhaseMismatch is returned when an operation is attempted with a
	// secret or result belonging to a different exporter phase.
	ErrPhaseMismatch = errors.New("netsec: exporter phase mismatch")

	// ErrInvalidSecret is returned when an exporter secret is missing.
	ErrInvalidSecret = errors.New("netsec: invalid exporter secret")

	// ErrInvalidLength is returned when the requested output length is
	// outside the HKDF-Expand-Label limits.
	ErrInvalidLength = errors.New("netsec: invalid exporter output length")

	// ErrInvalidLabel is returned when a label cannot be encoded as a TLS 1.3
	// HKDF-Expand-Label label.
	ErrInvalidLabel = errors.New("netsec: invalid exporter label")

	// ErrInvalidContext is returned when the exporter context is too long.
	ErrInvalidContext = errors.New("netsec: exporter context too long")

	// ErrNoLabel is returned when an export request omits the label argument.
	ErrNoLabel = errors.New("netsec: exporter label is required")

	// ErrNoLength is returned when an export request omits the output length.
	ErrNoLength = errors.New("netsec: exporter output length is required")

	// ErrVerificationFailed is returned when exported key material does not
	// match the expected value.
	ErrVerificationFailed = errors.New("netsec: exported key material verification failed")
)
