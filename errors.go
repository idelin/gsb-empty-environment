package netsec

import "fmt"

// ErrorCode identifies a stable, programmatic failure class.
type ErrorCode string

const (
	CodeInvalidJobID    ErrorCode = "invalid_job_id"
	CodeUnauthenticated ErrorCode = "unauthenticated"
	CodeNotFound        ErrorCode = "not_found"
	CodeForbidden       ErrorCode = "forbidden"
	CodeExpired         ErrorCode = "expired"
	CodeRateLimited     ErrorCode = "rate_limited"
	CodeInvalidExpiry   ErrorCode = "invalid_expiry"
	CodeUnsupported     ErrorCode = "unsupported_image"
	CodeInvalidImage    ErrorCode = "invalid_image"
	CodeInternal        ErrorCode = "internal"
)

// AccessError is returned for all access-control and validation failures.
//
// A single AccessError can match more than one sentinel error. In particular,
// a job belonging to another subject is reported as both ErrNotFound and
// ErrForbidden so callers cannot use error text to distinguish "does not
// exist" from "exists but belongs to somebody else".
type AccessError struct {
	Code    ErrorCode
	Message string
	aliases []ErrorCode
}

func (e *AccessError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is implements errors.Is.
func (e *AccessError) Is(target error) bool {
	t, ok := target.(*AccessError)
	if !ok || t == nil || e == nil {
		return false
	}
	if e.Code == t.Code {
		return true
	}
	for _, code := range e.aliases {
		if code == t.Code {
			return true
		}
	}
	return false
}

// Sentinel errors for use with errors.Is.
var (
	ErrInvalidJobID    = &AccessError{Code: CodeInvalidJobID, Message: "malformed job identifier"}
	ErrUnauthenticated = &AccessError{Code: CodeUnauthenticated, Message: "missing user or device identity"}
	ErrNotFound        = &AccessError{Code: CodeNotFound, Message: "job not found"}
	ErrForbidden       = &AccessError{Code: CodeForbidden, Message: "job does not belong to this subject"}
	ErrAccessDenied    = ErrForbidden
	ErrExpired         = &AccessError{Code: CodeExpired, Message: "job has expired"}
	ErrRateLimited     = &AccessError{Code: CodeRateLimited, Message: "too many failed job lookups"}
	ErrInvalidExpiry   = &AccessError{Code: CodeInvalidExpiry, Message: "invalid job expiry"}
	ErrUnsupported     = &AccessError{Code: CodeUnsupported, Message: "unsupported image format"}
	ErrInvalidImage    = &AccessError{Code: CodeInvalidImage, Message: "invalid image"}
	ErrInternal        = &AccessError{Code: CodeInternal, Message: "scan storage failure"}
)

// notAccessible deliberately aliases both not-found and forbidden.
var notAccessible = &AccessError{
	Code:    CodeNotFound,
	Message: "job not found or not accessible",
	aliases: []ErrorCode{CodeForbidden},
}

func invalidImage(formatName string) error {
	return &AccessError{
		Code:    CodeInvalidImage,
		Message: "invalid " + formatName + " image",
	}
}
