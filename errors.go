package admission

import (
	"errors"
	"fmt"
)

// ErrorCode classifies admission failures.  Every failure leaves the gate's
// committed state and history unchanged.
type ErrorCode string

const (
	CodeInvalidInput      ErrorCode = "invalid_input"
	CodeNonWellDefined    ErrorCode = "non_well_defined_homomorphism"
	CodeInconsistent      ErrorCode = "inconsistent_constraints"
	CodeCollusionExceeded ErrorCode = "collusion_policy_exceeded"
	CodeEnumerationLimit  ErrorCode = "enumeration_limit"
	CodeInternal          ErrorCode = "internal_error"
)

var (
	ErrInvalidInput      = errors.New("admission: invalid input")
	ErrNonWellDefined    = errors.New("admission: non-well-defined homomorphism")
	ErrInconsistent      = errors.New("admission: inconsistent constraints")
	ErrCollusionExceeded = errors.New("admission: collusion policy exceeded")
	ErrEnumerationLimit  = errors.New("admission: exact enumeration limit exceeded")
	ErrInternal          = errors.New("admission: internal error")
)

// AdmissionError carries enough context for a gateway to turn a rejected
// batch into a precise audit record.
type AdmissionError struct {
	Code    ErrorCode
	Op      string
	Policy  string
	Detail  string
	Wrapped error
}

func (e *AdmissionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := string(e.Code)
	if e.Op != "" {
		msg += " during " + e.Op
	}
	if e.Policy != "" {
		msg += " for policy " + e.Policy
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *AdmissionError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Wrapped != nil {
		return e.Wrapped
	}
	switch e.Code {
	case CodeInvalidInput:
		return ErrInvalidInput
	case CodeNonWellDefined:
		return ErrNonWellDefined
	case CodeInconsistent:
		return ErrInconsistent
	case CodeCollusionExceeded:
		return ErrCollusionExceeded
	case CodeEnumerationLimit:
		return ErrEnumerationLimit
	default:
		return ErrInternal
	}
}

func admissionError(code ErrorCode, op, policy, format string, args ...any) *AdmissionError {
	return &AdmissionError{
		Code:   code,
		Op:     op,
		Policy: policy,
		Detail: fmt.Sprintf(format, args...),
	}
}
