package scim

import "fmt"

// ErrorCode classifies failures returned by this package.
type ErrorCode string

const (
	// ErrCodeParse indicates the filter string is not valid SCIM syntax.
	ErrCodeParse ErrorCode = "parse_error"
	// ErrCodeUnauthorizedAttr indicates the filter, sort or projection
	// references an attribute the caller is not allowed to use.
	ErrCodeUnauthorizedAttr ErrorCode = "unauthorized_attribute"
	// ErrCodeOperatorNotAllowed indicates an operator is not permitted
	// on the attribute it is applied to.
	ErrCodeOperatorNotAllowed ErrorCode = "operator_not_allowed"
	// ErrCodeInvalidValue indicates a comparison literal failed validation.
	ErrCodeInvalidValue ErrorCode = "invalid_value"
	// ErrCodeNegationNotAllowed indicates a NOT expression in a position
	// where it could widen the result set.
	ErrCodeNegationNotAllowed ErrorCode = "negation_not_allowed"
	// ErrCodeTooComplex indicates the filter exceeds OR-branch or depth
	// limits configured in the Policy.
	ErrCodeTooComplex ErrorCode = "filter_too_complex"
	// ErrCodeInvalidSort indicates an unauthorized or malformed sort request.
	ErrCodeInvalidSort ErrorCode = "invalid_sort"
	// ErrCodeInvalidProjection indicates an unauthorized attributes or
	// excludedAttributes selection.
	ErrCodeInvalidProjection ErrorCode = "invalid_projection"
	// ErrCodeInvalidPagination indicates out-of-range pagination parameters.
	ErrCodeInvalidPagination ErrorCode = "invalid_pagination"
)

// Error is the typed error returned by Parse, Validate and Query.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("scim: %s: %s", e.Code, e.Msg) }

func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
