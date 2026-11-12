package netsec

import "errors"

// Errors returned for malformed inputs or contradictory clock synchronization
// data. They are returned directly (or wrapped) by Evaluate and the high-level
// helpers, never encoded in a Verdict.
var (
	// ErrInfeasibleOffsets reports a clock whose closed offset domain is
	// empty, i.e. the synchronization bounds contradict each other.
	ErrInfeasibleOffsets = errors.New("netsec: clock has an empty (contradictory) offset domain")

	// ErrInvalidWindow reports a half-open window whose lower bound is not
	// strictly below its upper bound, or whose bounds reference different
	// clocks.
	ErrInvalidWindow = errors.New("netsec: invalid half-open validity window")

	// ErrUnknownClock reports a window that references a clock missing from
	// Problem.Clocks.
	ErrUnknownClock = errors.New("netsec: window references an unregistered clock")

	// ErrDomainTooLarge reports a joint offset domain whose Cartesian
	// product exceeds maxCombinations. The package only supports exhaustive
	// checking of small integer domains by design.
	ErrDomainTooLarge = errors.New("netsec: joint offset domain exceeds exhaustive enumeration limit")
)

// Clock describes a single time source. Offsets are integer seconds and the
// true offset is known to lie in the closed interval [MinOffset, MaxOffset].
//
// Clocks are identified by pointer identity: every window referencing the same
// *Clock shares a single offset variable during evaluation.
type Clock struct {
	// Name identifies the clock in reports and errors.
	Name string

	// MinOffset and MaxOffset bound the unknown true offset in whole
	// seconds, both ends included.
	MinOffset int64
	MaxOffset int64
}

// Validate reports ErrInfeasibleOffsets when the closed offset domain is
// empty. A nil receiver is treated as an infeasible clock.
func (c *Clock) Validate() error {
	if c == nil || c.MinOffset > c.MaxOffset {
		return ErrInfeasibleOffsets
	}
	return nil
}

// Size returns the number of integer offsets in the closed domain. The second
// result is false when the domain is empty or its size cannot be represented
// in an int64.
func (c *Clock) Size() (int64, bool) {
	if c == nil || c.MinOffset > c.MaxOffset {
		return 0, false
	}
	span := c.MaxOffset - c.MinOffset
	if span < 0 || span > int64(^uint64(0)>>1) {
		return 0, false
	}
	return span + 1, true
}

// Window is a half-open validity interval [NotBefore, NotAfter) expressed in
// the readings of one clock.
type Window struct {
	NotBefore Bound
	NotAfter  Bound
}

// Bound is one endpoint of a Window: an integer-second reading of Clock.
type Bound struct {
	Clock   *Clock
	Reading int64
}

// Validate checks that both endpoints reference the same non-nil clock and
// that the half-open interval is non-empty.
func (w Window) Validate() error {
	if w.NotBefore.Clock == nil || w.NotAfter.Clock == nil {
		return ErrInvalidWindow
	}
	if w.NotBefore.Clock != w.NotAfter.Clock {
		return ErrInvalidWindow
	}
	if w.NotBefore.Reading >= w.NotAfter.Reading {
		return ErrInvalidWindow
	}
	return w.NotBefore.Clock.Validate()
}

// contains reports whether the clock reading at true second t, given the
// clock's assigned offset, falls inside the half-open window.
func (w Window) contains(trueTime, offset int64) bool {
	reading := trueTime + offset
	return w.NotBefore.Reading <= reading && reading < w.NotAfter.Reading
}
