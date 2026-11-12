package netsec

// Ticket validation error codes returned in CAS responses.
const (
	CodeInvalidRequest = "INVALID_REQUEST"
	CodeInvalidTicket  = "INVALID_TICKET"
	CodeInvalidService = "INVALID_SERVICE"
	CodeInternalError  = "INTERNAL_ERROR"
)

// ValidationError is returned when a service ticket cannot be accepted.
//
// Use errors.Is to test the reason, for example:
//
//	errors.Is(err, netsec.ErrTicketExpired)
type ValidationError struct {
	Code   string
	Reason string
}

func (e *ValidationError) Error() string {
	return e.Code + ": " + e.Reason
}

// Is reports whether the two errors describe the same validation failure.
func (e *ValidationError) Is(target error) bool {
	other, ok := target.(*ValidationError)
	return ok && e.Code == other.Code && e.Reason == other.Reason
}

var (
	// ErrTicketNotFound means the supplied value is not a ticket known to the
	// store. It is also used for malformed ticket identifiers.
	ErrTicketNotFound = &ValidationError{CodeInvalidTicket, "ticket not found"}

	// ErrTicketUsed means the service ticket has already been successfully
	// consumed. A ticket is never consumed by an unsuccessful validation.
	ErrTicketUsed = &ValidationError{CodeInvalidTicket, "ticket already used"}

	// ErrTicketExpired means the ticket is at or beyond its ten-second
	// lifetime.
	ErrTicketExpired = &ValidationError{CodeInvalidTicket, "ticket expired"}

	// ErrServiceMismatch means the service URL presented for validation does
	// not normalize to the service URL that was bound at issuance.
	ErrServiceMismatch = &ValidationError{CodeInvalidService, "service does not match ticket"}

	// ErrRenewRequired means renew=true was requested for a ticket that was
	// not issued from a fresh credential authentication.
	ErrRenewRequired = &ValidationError{CodeInvalidTicket, "ticket was not issued from credential authentication"}

	// ErrProxyTicket means a proxy ticket was submitted to the service-ticket
	// validation endpoint. Proxy tickets cannot be used as service tickets.
	ErrProxyTicket = &ValidationError{CodeInvalidTicket, "proxy ticket cannot be used as a service ticket"}

	// ErrServiceTicket means a service ticket was submitted to proxy-ticket
	// validation.
	ErrServiceTicket = &ValidationError{CodeInvalidTicket, "service ticket cannot be used as a proxy ticket"}

	// ErrInvalidService means the service URL is missing or malformed.
	ErrInvalidService = &ValidationError{CodeInvalidService, "invalid service URL"}

	// ErrInvalidTicket means a ticket argument is missing or malformed.
	ErrInvalidTicket = &ValidationError{CodeInvalidRequest, "invalid ticket request"}

	// ErrInvalidPrincipal means a ticket was issued for an empty principal.
	ErrInvalidPrincipal = &ValidationError{CodeInvalidRequest, "invalid principal"}
)

// Compatibility aliases for callers that use common alternative names.
var (
	ErrInvalidRequest         = ErrInvalidTicket
	ErrTicketAlreadyUsed      = ErrTicketUsed
	ErrUnknownTicket          = ErrTicketNotFound
	ErrRenewValidationFailed  = ErrRenewRequired
	ErrProxyTicketNotAccepted = ErrProxyTicket
)
