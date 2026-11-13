package netsec

import (
	"errors"
	"fmt"
)

// ErrInvalidLogoutToken is the common parent of all validation failures.
// Callers can use errors.Is(err, ErrInvalidLogoutToken) to reject a token
// without depending on a specific failure reason.
var ErrInvalidLogoutToken = errors.New("invalid logout token")

var (
	ErrMalformedToken          = fmt.Errorf("%w: malformed token", ErrInvalidLogoutToken)
	ErrUnsupportedAlgorithm    = fmt.Errorf("%w: unsupported signing algorithm", ErrInvalidLogoutToken)
	ErrUnsupportedCritical     = fmt.Errorf("%w: unsupported critical header", ErrInvalidLogoutToken)
	ErrKeyUnavailable          = fmt.Errorf("%w: signing key unavailable", ErrInvalidLogoutToken)
	ErrInvalidSignature        = fmt.Errorf("%w: signature verification failed", ErrInvalidLogoutToken)
	ErrIssuerMismatch          = fmt.Errorf("%w: issuer mismatch", ErrInvalidLogoutToken)
	ErrAudienceMismatch        = fmt.Errorf("%w: audience mismatch", ErrInvalidLogoutToken)
	ErrInvalidTokenType        = fmt.Errorf("%w: typ is not logout+jwt", ErrInvalidLogoutToken)
	ErrMissingLogoutEvent      = fmt.Errorf("%w: missing backchannel-logout event", ErrInvalidLogoutToken)
	ErrMissingSubjectOrSession = fmt.Errorf("%w: neither sub nor sid is present", ErrInvalidLogoutToken)
	ErrNoncePresent            = fmt.Errorf("%w: nonce must not be present", ErrInvalidLogoutToken)
	ErrMissingTokenID          = fmt.Errorf("%w: jti is missing", ErrInvalidLogoutToken)
	ErrReplayDetected          = fmt.Errorf("%w: jti was already seen within the replay window", ErrInvalidLogoutToken)
	ErrTokenExpired            = fmt.Errorf("%w: token is expired", ErrInvalidLogoutToken)
	ErrTokenNotYetValid        = fmt.Errorf("%w: token is not yet valid", ErrInvalidLogoutToken)
	ErrTokenIssuedInFuture     = fmt.Errorf("%w: token was issued in the future", ErrInvalidLogoutToken)
)
