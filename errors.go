package netsec

import "errors"

// Sentinel errors returned by the package.
var (
	// ErrMalformedInput indicates that the wire input does not strictly
	// match the required layout, including truncated fields, bad length
	// prefixes, or trailing bytes.
	ErrMalformedInput = errors.New("netsec: malformed hybrid input")

	// ErrUnknownSuite indicates that the suite ID has not been registered
	// with the registry.
	ErrUnknownSuite = errors.New("netsec: unknown suite id")

	// ErrInvalidSuiteConfig indicates that a suite was registered with an
	// invalid configuration, such as a missing adapter or a zero ciphertext
	// length.
	ErrInvalidSuiteConfig = errors.New("netsec: invalid suite configuration")

	// ErrSuiteAlreadyRegistered indicates that RegisterSuite was called
	// twice for the same suite ID.
	ErrSuiteAlreadyRegistered = errors.New("netsec: suite id already registered")

	// ErrDecapsulationFailed is the single generic error returned when one
	// or both components fail. It deliberately carries no information about
	// which component failed.
	ErrDecapsulationFailed = errors.New("netsec: hybrid decapsulation failed")
)
