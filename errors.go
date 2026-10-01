package strdict

import "errors"

var (
	ErrFrozen         = errors.New("strdict: dictionary is frozen")
	ErrNotFrozen      = errors.New("strdict: dictionary must be frozen before publish")
	ErrVersionExists  = errors.New("strdict: dictionary version already published")
	ErrUnknownVersion = errors.New("strdict: unknown dictionary version")
	ErrUnknownIndex   = errors.New("strdict: unknown dictionary index")
	ErrLeaseActive    = errors.New("strdict: dictionary has active read leases")
	ErrLeaseReleased  = errors.New("strdict: read lease has been released")
	ErrCancelled      = errors.New("strdict: batch cancelled")
	ErrBudgetExceeded = errors.New("strdict: byte budget exceeded")
	ErrLengthTooLarge = errors.New("strdict: declared length exceeds limit")
	ErrMalformed      = errors.New("strdict: malformed stream")
	ErrNoDictionary   = errors.New("strdict: no dictionary bound to decode session")
)
