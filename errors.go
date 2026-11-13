package netsec

import "errors"

var (
	// ErrInvalidLayout covers malformed IDs, roles, overlapping frames, etc.
	ErrInvalidLayout = errors.New("netsec: invalid layout")
	// ErrMappingOutOfRange means Locate succeeded but returned a coordinate
	// outside Layout.Limits.
	ErrMappingOutOfRange = errors.New("netsec: mapping outside limits")
	// ErrTooManyFreePages means more than MaxFreePages destinations were
	// supplied.
	ErrTooManyFreePages = errors.New("netsec: too many free pages")
	// ErrTooManyMovablePages means the layout has more than MaxMovablePages
	// movable attacker/sensitive pages.
	ErrTooManyMovablePages = errors.New("netsec: too many movable witness pages")
	// ErrNoSafePlacement means no final witness-free assignment exists even
	// without the online-order constraint.
	ErrNoSafePlacement = errors.New("netsec: no safe placement")
	// ErrNoOnlineOrder means a static placement exists, but no ordering of the
	// selected relocations has a non-expanding witness-triple set at every
	// intermediate step.
	ErrNoOnlineOrder = errors.New("netsec: no safe online migration order")
	// ErrBudgetExhausted returns the best known result and an unclosed lower
	// bound.
	ErrBudgetExhausted = errors.New("netsec: search budget exhausted")
)
