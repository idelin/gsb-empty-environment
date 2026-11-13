package netsec

import "errors"

var (
	ErrInvalidConfig          = errors.New("netsec: invalid issuer configuration")
	ErrInvalidRequest         = errors.New("netsec: invalid redemption request")
	ErrInvalidPolicy          = errors.New("netsec: invalid ticket policy")
	ErrTicketRejected         = errors.New("netsec: ticket rejected")
	ErrTicketExpired          = errors.New("netsec: ticket expired")
	ErrInvalidBlind           = errors.New("netsec: invalid blinding value")
	ErrBlindNotCoprime        = errors.New("netsec: blinding value is not coprime to modulus")
	ErrBlindMismatch          = errors.New("netsec: request was reserved with a different blinding value")
	ErrRequestConflict        = errors.New("netsec: request identifier conflicts with an earlier request")
	ErrQuotaExceeded          = errors.New("netsec: ticket redemption quota exhausted")
	ErrTicketPolicyMismatch   = errors.New("netsec: ticket policy changed after first reservation")
	ErrReservationNotFound    = errors.New("netsec: reservation not found")
	ErrSignatureConflict      = errors.New("netsec: conflicting signature for reservation")
	ErrSignerFailed           = errors.New("netsec: private key operation failed")
	ErrInvalidSignature       = errors.New("netsec: invalid blind signature")
	ErrInvalidTicketID        = errors.New("netsec: invalid ticket identifier")
	ErrInvalidClaimDigest     = errors.New("netsec: invalid claim digest")
	ErrMissingVerifier        = errors.New("netsec: missing ticket verifier")
	ErrMissingStore           = errors.New("netsec: missing redemption store")
	ErrMissingSigner          = errors.New("netsec: missing private key operation")
	ErrUnknownVerifier        = errors.New("netsec: unsupported ticket verifier")
	ErrStoreResultUnavailable = errors.New("netsec: store returned no reservation")
)
