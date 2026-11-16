package wssecurity

import (
	"errors"
	"fmt"
)

// Sentinel errors reported through ValidationError.Code. Use errors.Is to
// match them regardless of the wrapping message.
var (
	ErrMalformed              = errors.New("wssecurity: malformed SOAP message")
	ErrNoSecurityHeader       = errors.New("wssecurity: missing wsse:Security header")
	ErrNoTimestamp            = errors.New("wssecurity: missing wsu:Timestamp")
	ErrMultipleTimestamps     = errors.New("wssecurity: more than one wsu:Timestamp")
	ErrTimestampOutsideHeader = errors.New("wssecurity: wsu:Timestamp must be a descendant of wsse:Security")
	ErrNoBody                 = errors.New("wssecurity: missing SOAP Body")
	ErrMultipleBodies         = errors.New("wssecurity: more than one SOAP Body")
	ErrMissingID              = errors.New("wssecurity: required wsu:Id is missing")
	ErrDuplicateID            = errors.New("wssecurity: duplicate wsu:Id")
	ErrSignatureVerify        = errors.New("wssecurity: injected XML signature verification failed")
	ErrUnsignedTimestamp      = errors.New("wssecurity: wsu:Timestamp wsu:Id is not among the signed references")
	ErrUnsignedBody           = errors.New("wssecurity: SOAP Body wsu:Id is not among the signed references")
	ErrInvalidTime            = errors.New("wssecurity: timestamp field is not a valid xsd:dateTime")
	ErrCreatedInFuture        = errors.New("wssecurity: wsu:Created is more than 60 seconds in the future")
	ErrExpiresOrder           = errors.New("wssecurity: wsu:Expires must be later than wsu:Created")
	ErrTimestampLifetime      = errors.New("wssecurity: timestamp lifetime must not exceed 300 seconds")
	ErrTimestampExpired       = errors.New("wssecurity: wsu:Timestamp has expired")
	ErrMissingNonce           = errors.New("wssecurity: UsernameToken is missing wsse:Nonce")
	ErrMissingUTCreated       = errors.New("wssecurity: UsernameToken is missing wsu:Created")
	ErrReplayedNonce          = errors.New("wssecurity: UsernameToken Nonce/Created pair has already been used")
)

// ValidationError wraps a sentinel error with a human readable message.
// errors.Is(err, Err...) keeps working through Unwrap.
type ValidationError struct {
	Code error
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Msg == "" {
		return e.Code.Error()
	}
	return e.Code.Error() + ": " + e.Msg
}

func (e *ValidationError) Unwrap() error {
	return e.Code
}

func validationError(code error, format string, args ...any) *ValidationError {
	return &ValidationError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
