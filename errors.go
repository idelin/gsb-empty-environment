package netsec

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by [Validator.Validate]. Use errors.Is rather than
// equality so wrapped messages remain inspectable.
var (
	// ErrInvalidMessage wraps every malformed or non-well-formed SOAP message.
	ErrInvalidMessage = errors.New("wssecurity: invalid SOAP message")

	ErrMissingTimestamp   = errors.New("wssecurity: missing Timestamp in Security header")
	ErrDuplicateTimestamp = errors.New("wssecurity: multiple Timestamp elements")
	ErrMalformedTimestamp = errors.New("wssecurity: malformed Timestamp")
	ErrCreatedInFuture    = errors.New("wssecurity: Created is too far in the future")
	ErrInvalidTTL         = errors.New("wssecurity: invalid timestamp lifetime")
	ErrExpired            = errors.New("wssecurity: timestamp expired")

	ErrMissingTimestampID = errors.New("wssecurity: Timestamp has no wsu:Id")
	ErrMissingBodyID      = errors.New("wssecurity: SOAP Body has no wsu:Id")
	ErrDuplicateID        = errors.New("wssecurity: duplicate wsu:Id")
	ErrUnsignedTimestamp  = errors.New("wssecurity: Timestamp is not covered by the signature")
	ErrUnsignedBody       = errors.New("wssecurity: SOAP Body is not covered by the signature")

	ErrMalformedUsernameToken = errors.New("wssecurity: malformed UsernameToken")
	ErrReplay                 = errors.New("wssecurity: UsernameToken nonce replay")

	ErrSignatureVerify = errors.New("wssecurity: signature verification failed")
	ErrConfiguration   = errors.New("wssecurity: validator misconfigured")
)

// ValidationError pairs a stable sentinel with a human-readable cause. It
// unwraps to the sentinel so errors.Is keeps working for SOAP fault mapping.
type ValidationError struct {
	cause error
	err   error
}

func fail(sentinel error, format string, args ...any) error {
	return &ValidationError{
		cause: sentinel,
		err:   fmt.Errorf(format, args...),
	}
}

func (e *ValidationError) Error() string { return e.err.Error() }
func (e *ValidationError) Unwrap() error { return e.cause }
