package tokenexchange

import (
	"errors"
	"fmt"
)

// ErrorCode is an RFC 6749 / RFC 8693 token endpoint error code.
type ErrorCode string

const (
	// ErrInvalidRequest means the request is malformed or inconsistent.
	ErrInvalidRequest ErrorCode = "invalid_request"
	// ErrInvalidToken means the subject or actor token is invalid, expired,
	// revoked, or of an unexpected type.
	ErrInvalidToken ErrorCode = "invalid_token"
	// ErrInvalidScope means a requested scope exceeds the subject token.
	ErrInvalidScope ErrorCode = "invalid_scope"
	// ErrInvalidTarget means the requested audience is unknown or not allowed.
	ErrInvalidTarget ErrorCode = "invalid_target"
	// ErrAccessDenied means the requester is not authorized for the exchange.
	ErrAccessDenied ErrorCode = "access_denied"
)

// Error describes why an exchange request was rejected.
type Error struct {
	// Code is the RFC 6749 error code returned to the client.
	Code ErrorCode
	// Description is the optional human-readable error_description.
	Description string
}

func (e *Error) Error() string {
	if e.Description == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Description
}

// ErrorCodeOf extracts the token endpoint error code from a decision error.
func ErrorCodeOf(err error) (ErrorCode, bool) {
	var decisionErr *Error
	if errors.As(err, &decisionErr) {
		return decisionErr.Code, true
	}
	return "", false
}

func decisionError(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Description: fmt.Sprintf(format, args...)}
}
