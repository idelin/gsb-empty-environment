package netsec

import "errors"

var (
	// ErrInvalidScope is returned when a search has no usable tenant or an
	// otherwise malformed access scope.
	ErrInvalidScope = errors.New("netsec: invalid access scope")

	// ErrInvalidQuery is returned for malformed search parameters.
	ErrInvalidQuery = errors.New("netsec: invalid search query")

	// ErrUnsupported is returned for search features that cannot be evaluated
	// safely by this component. Failing closed is deliberate: silently
	// ignoring a filter can widen a search.
	ErrUnsupported = errors.New("netsec: unsupported search feature")

	// ErrTooDeep is returned when a chain or include expansion exceeds the
	// configured traversal limit.
	ErrTooDeep = errors.New("netsec: search expansion too deep")

	// ErrBudgetExceeded is returned when resolving references would exceed the
	// per-search resource budget.
	ErrBudgetExceeded = errors.New("netsec: search expansion budget exceeded")

	// ErrNotFound is returned by a Store when a resource does not exist in the
	// requested tenant.
	ErrNotFound = errors.New("netsec: resource not found")
)
