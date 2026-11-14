package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformedReferral indicates that a referral URL could not be parsed
	// according to the LDAP URL syntax.
	ErrMalformedReferral = errors.New("netsec: malformed referral")
	// ErrUnsupportedScheme indicates a URL scheme other than ldap or ldaps.
	ErrUnsupportedScheme = errors.New("netsec: unsupported referral scheme")
	// ErrUntrustedHost indicates that the referral host is outside the
	// configured trust domain.
	ErrUntrustedHost = errors.New("netsec: referral host is not trusted")
	// ErrPortNotAllowed indicates that the referral uses a port that is not
	// explicitly allowed for its scheme.
	ErrPortNotAllowed = errors.New("netsec: referral port is not allowed")
	// ErrCleartextDowngrade indicates an attempt to move from a TLS-protected
	// connection to a cleartext connection.
	ErrCleartextDowngrade = errors.New("netsec: refusal to downgrade referral to cleartext")
	// ErrReferralLoop indicates that a referral target was already visited.
	ErrReferralLoop = errors.New("netsec: referral loop detected")
	// ErrMaxReferralDepth indicates that the configured referral depth was
	// exceeded.
	ErrMaxReferralDepth = errors.New("netsec: maximum referral depth exceeded")
	// ErrCrossForest indicates that a referral crosses a configured forest
	// boundary while cross-forest referrals are disabled.
	ErrCrossForest = errors.New("netsec: cross-forest referral is not allowed")
	// ErrCredentialsNotAllowed indicates that the referral cannot be followed
	// because credentials may not be forwarded and anonymous following is
	// disabled.
	ErrCredentialsNotAllowed = errors.New("netsec: credentials may not be forwarded and anonymous follow is disabled")
	// ErrNotFollowable is returned when a referral fails policy evaluation.
	ErrNotFollowable = errors.New("netsec: referral is not followable")
	// ErrTLSRequired indicates that a cleartext LDAP connection was requested
	// without explicit permission.
	ErrTLSRequired = errors.New("netsec: TLS is required")
)

// FollowError describes a referral that was rejected by policy or a failure
// while following a permitted referral.
type FollowError struct {
	Op       string
	Referral string
	Decision Decision
	Err      error
}

func (e *FollowError) Error() string {
	switch {
	case e.Referral != "":
		return fmt.Sprintf("netsec: %s %q: %v", e.Op, e.Referral, e.Err)
	default:
		return fmt.Sprintf("netsec: %s: %v", e.Op, e.Err)
	}
}

func (e *FollowError) Unwrap() error { return e.Err }

// LDAPError reports a non-success LDAP result code.
type LDAPError struct {
	Code       int
	Diagnostic string
	Referrals  []string
}

func (e *LDAPError) Error() string {
	if e.Diagnostic != "" {
		return fmt.Sprintf("netsec: LDAP result code %d: %s", e.Code, e.Diagnostic)
	}
	return fmt.Sprintf("netsec: LDAP result code %d", e.Code)
}
