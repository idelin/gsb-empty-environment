package takeover

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidRange      = errors.New("takeover: invalid sequence range")
	ErrInvalidDimension  = errors.New("takeover: invalid terminal dimension")
	ErrInvalidGrant      = errors.New("takeover: invalid grant")
	ErrGrantExists       = errors.New("takeover: grant already exists")
	ErrGrantNotFound     = errors.New("takeover: grant not found")
	ErrGrantInactive     = errors.New("takeover: grant is not active")
	ErrNotHolder         = errors.New("takeover: process is not the grant holder")
	ErrSessionMismatch   = errors.New("takeover: session root mismatch")
	ErrSignalNotAllowed  = errors.New("takeover: signal is not allowed")
	ErrDimensionNotOwned = errors.New("takeover: terminal dimension is not owned")
	ErrSequenceNotOwned  = errors.New("takeover: sequence is not owned")
	ErrSequenceUsed      = errors.New("takeover: sequence has already been used")
	ErrSequenceSealed    = errors.New("takeover: sequence has been sealed")
	ErrSeqDimensionScope = errors.New("takeover: return scope is not held")
	ErrUnsettled         = errors.New("takeover: descendants are not settled")
	ErrInvalidReceipt    = errors.New("takeover: invalid return receipt")
	ErrReceiptSignature  = errors.New("takeover: invalid receipt signature")
	ErrReceiptConflict   = errors.New("takeover: receipt conflicts with an existing operation")
	ErrEffectTransition  = errors.New("takeover: illegal effect transition")
	ErrPartition         = errors.New("takeover: invalid partition")
	ErrDispatchInDoubt   = errors.New("takeover: dispatch outcome is in doubt")
)

// DimensionConflictError reports overlapping dimensions.
type DimensionConflictError struct {
	A Dimension
	B Dimension
}

func (e *DimensionConflictError) Error() string {
	return fmt.Sprintf("takeover: dimensions overlap: %s/%s and %s/%s",
		e.A.DisplayID, e.A.WindowID, e.B.DisplayID, e.B.WindowID)
}

// UnsettledError lists descendants that still need a signed receipt or seal.
type UnsettledError struct {
	GrantIDs []GrantID
}

func (e *UnsettledError) Error() string {
	return fmt.Sprintf("%v: %v", ErrUnsettled, e.GrantIDs)
}

func (e *UnsettledError) Unwrap() error {
	return ErrUnsettled
}

// ValidationError names the grant or request field that failed validation.
type ValidationError struct {
	Field string
	Err   error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("takeover: invalid %s: %v", e.Field, e.Err)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}
