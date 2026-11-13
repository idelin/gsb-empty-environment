package grant

import (
	"errors"
	"fmt"
)

var (
	// ErrDenied is the root cause for every authorization rejection.
	ErrDenied = errors.New("grant: authorization denied")
	// ErrTokenInvalid means the token failed parsing or HMAC verification.
	ErrTokenInvalid = errors.New("grant: invalid token")
	// ErrTokenExpired means the token is past its expiry.
	ErrTokenExpired = errors.New("grant: token expired")
	// ErrRetired means the grant has been retired.
	ErrRetired = errors.New("grant: grant retired")
	// ErrPendingWindow means pending propagation is not permitted or has closed.
	ErrPendingWindow = errors.New("grant: pending propagation window closed")
	// ErrPermission means the host authority denied Grant permission.
	ErrPermission = errors.New("grant: Grant permission denied")
	// ErrConflict means an idempotency key is bound to a different request.
	ErrConflict = errors.New("grant: conflicting request id")
	// ErrNotFound means no grant matches the lookup.
	ErrNotFound = errors.New("grant: not found")
	// ErrInvalidRequest means the Create/Authorize request is malformed.
	ErrInvalidRequest = errors.New("grant: invalid request")
)

// deny wraps ErrDenied with a concrete reason while preserving errors.Is.
func deny(reason error, format string, args ...any) error {
	return errors.Join(reason, ErrDenied, fmt.Errorf(format, args...))
}
