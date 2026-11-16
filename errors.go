package netsec

import "errors"

// Admission and authentication errors. Callers should use errors.Is rather
// than comparing error strings.
var (
	// ErrSuiteRejected is the common error returned for any cipher suite that
	// is not permitted by policy.
	ErrSuiteRejected = errors.New("netsec: cipher suite rejected")

	// ErrAuthAlgorithmNone means the suite's RAKP authentication algorithm is
	// 0 (none).
	ErrAuthAlgorithmNone = errors.New("netsec: authentication algorithm none is forbidden")

	// Deprecated: use ErrAuthAlgorithmNone.
	ErrAuthenticationNone = ErrAuthAlgorithmNone

	// ErrConfidentialityNone means the suite's confidentiality algorithm is
	// 0 (none).
	ErrConfidentialityNone = errors.New("netsec: confidentiality algorithm none is forbidden")

	// Deprecated: use ErrConfidentialityNone.
	ErrConfidentialityAlgorithmNone = ErrConfidentialityNone

	// ErrSuiteNotRegistered means the suite number was not registered by the
	// caller.
	ErrSuiteNotRegistered = errors.New("netsec: cipher suite not registered")

	// ErrUnsupportedAlgorithm means the suite names an authentication
	// algorithm this package cannot implement.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

	// ErrAnonymous means the login used an empty username and empty password.
	ErrAnonymous = errors.New("netsec: anonymous login rejected")

	// ErrAuthenticationFailed is deliberately identical for an unknown user
	// and for a bad password, so callers cannot use the error to enumerate
	// users.
	ErrAuthenticationFailed = errors.New("netsec: RAKP authentication failed")

	// ErrThrottled means the source address has exceeded the RAKP failure
	// budget for the sliding window.
	ErrThrottled = errors.New("netsec: source address throttled")

	// Deprecated: use ErrThrottled.
	ErrSourceThrottled = ErrThrottled

	// ErrInvalidRequest means the RAKP state machine was called out of order
	// or with a malformed request.
	ErrInvalidRequest = errors.New("netsec: invalid RAKP request")
)
