package evidence

import "errors"

// Sentinel errors.  Public operations wrap these with details, so callers
// should use errors.Is rather than comparing error strings.
var (
	ErrInvalidIP           = errors.New("evidence: invalid IP address")
	ErrReservedIP          = errors.New("evidence: IP is not a global public address")
	ErrInvalidOrder        = errors.New("evidence: invalid order")
	ErrInvalidWitness      = errors.New("evidence: invalid witness")
	ErrOrderMismatch       = errors.New("evidence: witness does not match order")
	ErrAddressMismatch     = errors.New("evidence: connected address does not match order")
	ErrMissingIdentity     = errors.New("evidence: missing identity attribution")
	ErrAmbiguousIdentity   = errors.New("evidence: ambiguous identity attribution")
	ErrIdentityConflict    = errors.New("evidence: conflicting identity lists")
	ErrRevoked             = errors.New("evidence: evidence was revoked at issuance time")
	ErrNoCommonInstant     = errors.New("evidence: no common observation instant")
	ErrInsufficientDomains = errors.New("evidence: insufficient distinct management domains")
	ErrRouteOriginConflict = errors.New("evidence: duplicate route origin")
	ErrForkedReceipt       = errors.New("evidence: forked receipt")
	ErrNoValidEvidence     = errors.New("evidence: no valid evidence set")
	ErrInvalidBundle       = errors.New("evidence: invalid evidence bundle")
	ErrSearchLimit         = errors.New("evidence: selection search limit exceeded")
	ErrInvalidDigest       = errors.New("evidence: invalid digest")
	ErrInvalidTimeSpan     = errors.New("evidence: invalid time span")
)
