package krb5policy

import "errors"

// Sentinel errors.  Public APIs wrap these with %w and contextual text,
// so callers should use errors.Is rather than string matching.
var (
	ErrInvalidConfig = errors.New("krb5policy: invalid configuration")
	ErrInvalidRealm  = errors.New("krb5policy: invalid realm or principal")

	ErrTransitedEncoding  = errors.New("krb5policy: unsupported transited encoding")
	ErrTransitedMalformed = errors.New("krb5policy: malformed transited field")
	ErrTransitedLimit     = errors.New("krb5policy: transited field exceeds a configured bound")

	ErrPathLimit = errors.New("krb5policy: trust path exceeds a configured bound")
	ErrNoTrust   = errors.New("krb5policy: no directed trust edge")

	ErrPathNotAuthorized = errors.New("krb5policy: transited path is not authorized")
	ErrCapathViolation   = errors.New("krb5policy: capaths policy violated")
	ErrTransitShortcut   = errors.New("krb5policy: unauthorized cross-realm shortcut")
	ErrRealmLoop         = errors.New("krb5policy: realm loop in transited path")
	ErrIncompleteTransit = errors.New("krb5policy: transited path does not reach the service realm")

	ErrIntegrity      = errors.New("krb5policy: ticket integrity check failed")
	ErrPACIntegrity   = errors.New("krb5policy: PAC integrity check failed")
	ErrUnknownKey     = errors.New("krb5policy: no usable key for principal/version")
	ErrUnsupportedMAC = errors.New("krb5policy: unsupported MAC algorithm")

	ErrInvalidTicketWindow = errors.New("krb5policy: invalid ticket time window")
	ErrTicketExpired       = errors.New("krb5policy: ticket is expired")
	ErrTicketNotYetValid   = errors.New("krb5policy: ticket is not yet valid")
	ErrRenewalWindow       = errors.New("krb5policy: ticket is outside its renewal window")

	ErrProtectedPrincipal     = errors.New("krb5policy: protected principal cannot enter a delegation chain")
	ErrDelegationDenied       = errors.New("krb5policy: delegation is not permitted")
	ErrTargetNotAllowed       = errors.New("krb5policy: target is absent from AllowedToDelegateTo")
	ErrEvidenceNotForwardable = errors.New("krb5policy: S4U2Self evidence ticket is not forwardable")
	ErrFlagEscalation         = errors.New("krb5policy: derived ticket invents or strips a security flag")
	ErrTimeExpansion          = errors.New("krb5policy: derived ticket expands its time bounds")

	ErrSearchLimit = errors.New("krb5policy: adversarial search exceeded its configured bound")
)
