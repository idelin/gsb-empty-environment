package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformedHeader indicates that a tracing header did not match the
	// accepted wire format.
	ErrMalformedHeader = errors.New("netsec: malformed trace header")
	// ErrHeaderTooLong indicates that a tracing header exceeded a hard limit.
	ErrHeaderTooLong = errors.New("netsec: trace header too long")
	// ErrDuplicateHeader indicates that a tracing header occurred more than
	// once in a request. Ambiguous headers are never merged.
	ErrDuplicateHeader = errors.New("netsec: duplicate trace header")
	// ErrUnsupportedVersion indicates an unsupported traceparent version.
	ErrUnsupportedVersion = errors.New("netsec: unsupported traceparent version")
	// ErrInvalidSignature indicates that a trace context failed authentication.
	ErrInvalidSignature = errors.New("netsec: invalid trace context signature")
	// ErrExpiredContext indicates that a signed trace context is too old.
	ErrExpiredContext = errors.New("netsec: expired trace context")
	// ErrFutureContext indicates that a signed trace context is from the
	// future beyond the configured clock skew.
	ErrFutureContext = errors.New("netsec: trace context timestamp is in the future")
	// ErrBindingMismatch indicates that signed tenant or origin metadata did
	// not match the authenticated request binding.
	ErrBindingMismatch = errors.New("netsec: trace context binding mismatch")
	// ErrMissingBinding indicates that no authenticated tenant and origin
	// binding is available.
	ErrMissingBinding = errors.New("netsec: missing trace context binding")
	// ErrInvalidContext indicates that an in-memory trace context is invalid.
	ErrInvalidContext = errors.New("netsec: invalid trace context")
	// ErrNoTraceContext indicates that no trace context was attached to a
	// context.Context.
	ErrNoTraceContext = errors.New("netsec: no trace context in context")
)

// HeaderError identifies the tracing header that failed validation and wraps a
// sentinel reason suitable for errors.Is.
type HeaderError struct {
	Header string
	Reason error
	Detail string
}

func (e *HeaderError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("netsec: %s: %v", e.Header, e.Reason)
	}
	return fmt.Sprintf("netsec: %s: %v (%s)", e.Header, e.Reason, e.Detail)
}

func (e *HeaderError) Unwrap() error {
	return e.Reason
}

func headerError(header string, reason error, detail string) error {
	return &HeaderError{Header: header, Reason: reason, Detail: detail}
}
