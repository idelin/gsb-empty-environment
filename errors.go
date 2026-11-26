package temporal

import (
	"errors"
	"fmt"
)

// Sentinel errors used by the package. Callers can use errors.Is to detect
// constraint violations and rollback conditions.
var (
	// ErrKindMismatch is returned when ranges from different domains
	// (discrete vs continuous) are combined.
	ErrKindMismatch = errors.New("temporal: range kind mismatch")

	// ErrOverlap is the exclusion-constraint violation sentinel.
	ErrOverlap = errors.New("temporal: exclusion constraint violation: overlapping ranges for equal key")

	// ErrEmptyRange is returned when a row with an empty validity range is
	// inserted into a table that rejects empty ranges.
	ErrEmptyRange = errors.New("temporal: empty validity range rejected")

	// ErrCoverage is the foreign-key temporal coverage violation sentinel.
	ErrCoverage = errors.New("temporal: foreign key temporal coverage violation")

	// ErrUnknownTable is returned when an operation names a table absent
	// from a Schema/DB.
	ErrUnknownTable = errors.New("temporal: unknown table")

	// ErrNotFound is returned by lookup helpers.
	ErrNotFound = errors.New("temporal: not found")

	// ErrTxDone is returned when Commit/Rollback/use follows completion.
	ErrTxDone = errors.New("temporal: transaction already committed or rolled back")
)

// ExclusionViolation describes one pair of rows violating an exclusion
// constraint.
type ExclusionViolation struct {
	Constraint string
	Table      string
	Key        Key
	Left       RowRef
	Right      RowRef
	Overlap    Range
}

// RowRef is a lightweight reference to a row involved in a violation.
type RowRef struct {
	ID    string
	Index int
	Range Range
}

func (v *ExclusionViolation) Error() string {
	return fmt.Sprintf("%s: rows %s and %s overlap on %s for key %v",
		v.Constraint, refLabel(v.Left), refLabel(v.Right), v.Overlap, v.Key)
}

func refLabel(r RowRef) string {
	if r.ID != "" {
		return fmt.Sprintf("%q", r.ID)
	}
	return fmt.Sprintf("#%d", r.Index)
}

// Is lets errors.Is match ErrOverlap.
func (v *ExclusionViolation) Is(target error) bool { return target == ErrOverlap }

// Unwrap returns ErrOverlap.
func (v *ExclusionViolation) Unwrap() error { return ErrOverlap }

// CoverageViolation describes a child row whose validity interval is not
// fully covered by matching parent rows.
type CoverageViolation struct {
	Constraint string
	ChildTable string
	Child      RowRef
	ChildKey   Key
	Missing    []Range
}

func (v *CoverageViolation) Error() string {
	return fmt.Sprintf("%s: child row %s interval %s is not fully covered by parent %s; uncovered: %v",
		v.Constraint, refLabel(v.Child), v.Child.Range, v.Constraint, v.Missing)
}

// Is lets errors.Is match ErrCoverage.
func (v *CoverageViolation) Is(target error) bool { return target == ErrCoverage }

// Unwrap returns ErrCoverage.
func (v *CoverageViolation) Unwrap() error { return ErrCoverage }

// ValidationError aggregates all constraint violations found in one pass.
// UpdatePortion treats any non-empty ValidationError as a rollback trigger.
type ValidationError struct {
	Exclusion []*ExclusionViolation
	Coverage  []*CoverageViolation
	Empties   []EmptyRangeError
}

// EmptyRangeError identifies a stored row whose range is empty.
type EmptyRangeError struct {
	Table string
	Ref   RowRef
}

func (e EmptyRangeError) Error() string {
	return fmt.Sprintf("%s: row %s has empty validity range", ErrEmptyRange, refLabel(e.Ref))
}

// Is lets errors.Is match ErrEmptyRange.
func (e EmptyRangeError) Is(target error) bool { return target == ErrEmptyRange }

// Unwrap returns ErrEmptyRange.
func (e EmptyRangeError) Unwrap() error { return ErrEmptyRange }

// Error implements error.
func (v *ValidationError) Error() string {
	return fmt.Sprintf("temporal: validation failed with %d exclusion, %d coverage and %d empty-range violations",
		len(v.Exclusion), len(v.Coverage), len(v.Empties))
}

// Unwrap allows errors.Is to match any aggregated sentinel.
func (v *ValidationError) Unwrap() []error {
	var errs []error
	for _, e := range v.Exclusion {
		errs = append(errs, e)
	}
	for _, e := range v.Coverage {
		errs = append(errs, e)
	}
	for _, e := range v.Empties {
		errs = append(errs, e)
	}
	return errs
}

// HasViolations reports whether any violation was collected.
func (v *ValidationError) HasViolations() bool {
	return v != nil && (len(v.Exclusion) > 0 || len(v.Coverage) > 0 || len(v.Empties) > 0)
}

// OrNil returns v if it contains violations, otherwise nil, making it
// convenient to return as an error.
func (v *ValidationError) OrNil() error {
	if v == nil || !v.HasViolations() {
		return nil
	}
	return v
}
