package netsec

import "strings"

// Policy controls which requests may be admitted as early data. Its
// Version is embedded into every issued ticket; tightening the policy
// bumps the version and thereby invalidates all previously issued
// tickets.
type Policy struct {
	// Version is bumped by Gate.Tighten; tickets carrying an older
	// version are rejected.
	Version uint64
	// AllowIdempotentEarly admits idempotent methods (GET, HEAD, ...)
	// as early data without an idempotency key.
	AllowIdempotentEarly bool
	// RequireIdempotencyKey defers non-idempotent methods that arrive
	// without a one-time idempotency key until the handshake completes.
	RequireIdempotencyKey bool
}

// DefaultPolicy is the recommended starting posture: idempotent methods
// may fly early, everything else needs a one-time idempotency key.
func DefaultPolicy() Policy {
	return Policy{
		Version:               1,
		AllowIdempotentEarly:  true,
		RequireIdempotencyKey: true,
	}
}

// IsIdempotentMethod reports whether repeating the method is safe per
// RFC 9110 semantics.
func IsIdempotentMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS", "TRACE", "PUT", "DELETE":
		return true
	default:
		return false
	}
}
