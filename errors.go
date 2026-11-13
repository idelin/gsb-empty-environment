package netsec

import "errors"

var (
	// ErrEMSNegotiationMismatch is returned when exactly one side advertises
	// the extended_master_secret extension. The enforced policy requires
	// symmetric negotiation: either both sides use EMS or neither does.
	ErrEMSNegotiationMismatch = errors.New("netsec: extended_master_secret negotiation mismatch: extension offered by only one side")

	// ErrEMSResumptionLost is returned when a session originally established
	// with the extended master secret is being resumed without EMS.
	ErrEMSResumptionLost = errors.New("netsec: cannot resume an extended_master_secret session without the extended_master_secret flag")

	// ErrEMSResumptionUnexpected is returned when a session originally
	// established without EMS is resumed with EMS negotiated.
	ErrEMSResumptionUnexpected = errors.New("netsec: cannot resume a non-extended_master_secret session with the extended_master_secret flag")

	// ErrSessionUnknown is returned when no stored session matches a ticket.
	ErrSessionUnknown = errors.New("netsec: unknown or revoked session")

	// ErrSessionExpired is returned when the session referenced by a ticket
	// has outlived its negotiated lifetime.
	ErrSessionExpired = errors.New("netsec: session has expired")

	// ErrTicketInvalid is returned when a resumption ticket cannot be
	// authenticated or decoded.
	ErrTicketInvalid = errors.New("netsec: invalid or corrupted resumption ticket")
)
