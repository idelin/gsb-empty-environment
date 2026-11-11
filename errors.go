package wiretypes

import (
	"errors"
	"fmt"
)

// Sentinel errors. Every decoding failure wraps one of these so callers can
// classify failures with errors.Is even when extra context is attached.
var (
	// ErrEmptyMessage is returned when the message carries no objects.
	ErrEmptyMessage = errors.New("wiretypes: empty message")
	// ErrDuplicateID is returned when two table entries share a RefID.
	ErrDuplicateID = errors.New("wiretypes: duplicate object id")
	// ErrUnknownRoot is returned when the root RefID is absent from the table.
	ErrUnknownRoot = errors.New("wiretypes: root id not found")
	// ErrUnknownType is returned when a TypeID is not in the registry snapshot.
	ErrUnknownType = errors.New("wiretypes: unregistered type")
	// ErrTypeNotNegotiated is returned when the mode forbids a TypeID.
	ErrTypeNotNegotiated = errors.New("wiretypes: type not allowed by negotiated mode")
	// ErrUnknownRef is returned when a reference points at a missing entry.
	ErrUnknownRef = errors.New("wiretypes: dangling reference")
	// ErrTypeMismatch is returned when a value's kind, struct field or a
	// referenced object's type does not satisfy its field spec.
	ErrTypeMismatch = errors.New("wiretypes: type mismatch")
	// ErrUnknownField is returned when the wire carries an undeclared field.
	ErrUnknownField = errors.New("wiretypes: unknown field")
	// ErrMissingField is returned when a required field is absent.
	ErrMissingField = errors.New("wiretypes: missing required field")
	// ErrNullNotAllowed is returned when a null hits a non-nullable field.
	ErrNullNotAllowed = errors.New("wiretypes: null value for non-nullable field")
	// ErrBudgetExceeded is returned when any resource budget is exceeded.
	ErrBudgetExceeded = errors.New("wiretypes: resource budget exceeded")
	// ErrRejected is returned when the business callback rejects the message.
	ErrRejected = errors.New("wiretypes: message rejected by callback")
	// ErrRegistry is returned when a registry operation is invalid.
	ErrRegistry = errors.New("wiretypes: invalid registry")
)

// Error is a structured decoding failure.
type Error struct {
	// Op is the decoding phase where the failure occurred.
	Op string
	// Ref is the offending table entry, empty when the failure is global.
	Ref RefID
	// Field is the offending field path, empty when not field specific.
	Field string
	// Err wraps the classifying sentinel error.
	Err error
}

func (e *Error) Error() string {
	msg := "wiretypes: " + e.Op
	if e.Ref != "" {
		msg += " object=" + string(e.Ref)
	}
	if e.Field != "" {
		msg += " field=" + e.Field
	}
	return msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func decodeError(op string, ref RefID, field string, sentinel error, format string, args ...any) error {
	return &Error{
		Op:    op,
		Ref:   ref,
		Field: field,
		Err:   fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...)),
	}
}
