package reuseauth

import "errors"

// Stable errors are safe for the trusted browser host to classify. Navigation
// facing handlers should collapse authorization/existence failures to
// ErrNotFound, as Broker methods already do at their public boundary.
var (
	ErrInvalidArgument = errors.New("reuseauth: invalid argument")
	ErrNotFound        = errors.New("reuseauth: not found")
	ErrRevoked         = errors.New("reuseauth: navigation authorization revoked")
	ErrEpoch           = errors.New("reuseauth: stale navigation epoch")
	ErrOrigin          = errors.New("reuseauth: recipient origin mismatch")
	ErrPublicFlag      = errors.New("reuseauth: public flag mismatch")
	ErrPolicyDenied    = errors.New("reuseauth: origin policy denied")
	ErrSignature       = errors.New("reuseauth: invalid signature")
	ErrChain           = errors.New("reuseauth: invalid signed redirect chain")
	ErrDigestMismatch  = errors.New("reuseauth: digest mismatch")
	ErrConsumed        = errors.New("reuseauth: hit token already consumed")
	ErrLength          = errors.New("reuseauth: invalid or conflicting length")
)

// NavigationFacingError collapses private-cache lookup failures to the same
// not-found result used for a genuinely absent download.
func NavigationFacingError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// opaqueLookupError prints only ErrNotFound while retaining the detailed
// authorization cause for trusted host code via errors.Is/As.
type opaqueLookupError struct {
	cause error
}

func (e *opaqueLookupError) Error() string { return ErrNotFound.Error() }

func (e *opaqueLookupError) Unwrap() []error {
	return []error{ErrNotFound, e.cause}
}

func invisibleHitError(cause error) error {
	if cause == nil {
		return ErrNotFound
	}
	return &opaqueLookupError{cause: cause}
}
