package policymv

import "errors"

var (
	// ErrCycle is returned when applying a change set would create an
	// inheritance cycle (including a self loop).
	ErrCycle = errors.New("policymv: change set creates an inheritance cycle")

	// ErrMissingEdge is returned when removing an edge that does not exist.
	ErrMissingEdge = errors.New("policymv: cannot remove missing edge")

	// ErrDuplicateEdge is returned when adding an edge that already exists.
	ErrDuplicateEdge = errors.New("policymv: cannot add duplicate edge")

	// ErrMissingGrant is returned when revoking a permission that is absent.
	ErrMissingGrant = errors.New("policymv: cannot remove missing grant")

	// ErrDuplicateGrant is returned when granting an existing permission.
	ErrDuplicateGrant = errors.New("policymv: cannot add duplicate grant")

	// ErrUnknownVersion is returned when a base version id does not exist.
	ErrUnknownVersion = errors.New("policymv: unknown base version")

	// ErrStaleBase is returned by Publish when the base is not the current
	// version; callers should use Submit to rebase and auto-merge.
	ErrStaleBase = errors.New("policymv: base version is not current")

	// ErrDependencyConflict is returned when two change sets based on the
	// same old version have intersecting dependency closures.
	ErrDependencyConflict = errors.New("policymv: overlapping dependency closures, auto-merge rejected")
)
