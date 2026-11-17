package descriptor

import (
	"errors"
	"fmt"
)

var (
	// ErrSchema reports an invalid caller-provided instruction schema.
	ErrSchema = errors.New("descriptor: invalid schema")
	// ErrBadDescriptor reports malformed untrusted descriptor data.
	ErrBadDescriptor = errors.New("descriptor: invalid descriptor")
	// ErrUnknownOpcode reports an entry that matches no allowed pattern.
	ErrUnknownOpcode = errors.New("descriptor: unknown opcode")
	// ErrTail reports a trailing partial entry or a byte length that does not
	// divide into whole entries.
	ErrTail = errors.New("descriptor: invalid trailing entry")
	// ErrUnauthorized reports an access outside the caller's policy.
	ErrUnauthorized = errors.New("descriptor: access outside authorized region")
	// ErrCycle reports a cycle in the indirect table graph.
	ErrCycle = errors.New("descriptor: indirect table cycle")
	// ErrLimit reports an exceeded validation bound.
	ErrLimit = errors.New("descriptor: validation limit exceeded")
	// ErrPage reports a page-store or translation failure.
	ErrPage = errors.New("descriptor: page access failed")
	// ErrOverflow reports an arithmetic overflow in descriptor data.
	ErrOverflow = errors.New("descriptor: integer overflow")
	// ErrNil reports a missing required dependency or receiver.
	ErrNil = errors.New("descriptor: nil dependency")
	// ErrFreeze reports a failure while freezing a referenced page.
	ErrFreeze = errors.New("descriptor: page freeze failed")
)

// Error annotates a validation failure with its location.
type Error struct {
	Kind    error
	Address Address
	Entry   Address
	Field   string
	Detail  string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Kind.Error()
	if e.Address != 0 {
		msg += fmt.Sprintf(" at %#x", uint64(e.Address))
	}
	if e.Entry != 0 {
		msg += fmt.Sprintf(" entry %#x", uint64(e.Entry))
	}
	if e.Field != "" {
		msg += fmt.Sprintf(" field %q", e.Field)
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Cause != nil && !errors.Is(e.Cause, e.Kind) {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	if errors.Is(e.Kind, target) {
		return true
	}
	return e.Cause != nil && errors.Is(e.Cause, target)
}
