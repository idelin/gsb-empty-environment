package netsec

import "errors"

var (
	ErrCodeNotFound        = errors.New("netsec: verification code not found")
	ErrCodeMismatch        = errors.New("netsec: verification code mismatch")
	ErrCodeExpired         = errors.New("netsec: verification code expired")
	ErrCodeConsumed        = errors.New("netsec: verification code already consumed")
	ErrTooManyAttempts     = errors.New("netsec: too many verify attempts, code invalidated")
	ErrSendTooFrequent     = errors.New("netsec: resend interval not elapsed")
	ErrSendLimitExceeded   = errors.New("netsec: send limit exceeded for window")
	ErrRebindLimitExceeded = errors.New("netsec: phone rebind limit exceeded for window")
)
