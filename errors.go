package recovery

import "errors"

var (
	// ErrInvalidInput reports a structurally malformed request.
	ErrInvalidInput = errors.New("recovery: invalid input")

	// ErrUnsupportedSize reports a request beyond the package limits (60
	// logical volumes, four candidates per volume).
	ErrUnsupportedSize = errors.New("recovery: unsupported input size")

	// ErrBoundaryWidth reports a frontier wider than the configured limit.
	ErrBoundaryWidth = errors.New("recovery: boundary width exceeds supported limit")

	// ErrRankOutOfRange reports rank < 1 or rank > optimal combination count.
	ErrRankOutOfRange = errors.New("recovery: rank out of range")

	// ErrVerificationFailed reports a certificate/observation mismatch.
	ErrVerificationFailed = errors.New("recovery: certificate verification failed")
)
