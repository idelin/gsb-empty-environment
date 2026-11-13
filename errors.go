package netsec

import "errors"

var (
	// ErrInvalidConfig reports a provider configuration that cannot be
	// published. Callers should treat it as a configuration error.
	ErrInvalidConfig = errors.New("netsec: invalid provider configuration")

	// ErrCollision reports that two distinct claim tuples map to the same
	// principal. Publish returns a CollisionError wrapping this sentinel.
	ErrCollision = errors.New("netsec: claim tuple collision")

	// ErrAuditBudget reports that the collision audit could not close the
	// complete finite search within the configured budget.
	ErrAuditBudget = errors.New("netsec: collision audit budget exhausted")

	// ErrClaims reports a missing, malformed, empty, or disallowed claim.
	ErrClaims = errors.New("netsec: invalid claims")

	// ErrPrincipalTooLong reports a principal exceeding MaxPrincipalBytes.
	ErrPrincipalTooLong = errors.New("netsec: principal exceeds size limit")

	// ErrPrincipalBinding reports a principal that was not issued by the
	// policy generation handling the authorization request.
	ErrPrincipalBinding = errors.New("netsec: principal is bound to another policy")

	// ErrVerification reports that the host token verifier was nil or
	// returned an error. The host's error remains available through Unwrap.
	ErrVerification = errors.New("netsec: token verification failed")
)
