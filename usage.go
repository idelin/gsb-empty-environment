package settle

import (
	"errors"
	"math/big"
	"time"
)

// Usage is a quantity of consumption to be allocated to rate segments.
type Usage interface {
	// allocate returns the exact quantity of this usage that falls inside
	// the given segment.
	allocate(seg Segment) *big.Rat
	// span returns the half-open time span the usage occupies; instant
	// usages report a zero-length span at their event time.
	span() (start, end time.Time)
}

// Instant is a point-in-time quantity, billed at the rate of the segment
// containing At.
type Instant struct {
	At  time.Time
	Qty *big.Rat
}

func (u Instant) allocate(seg Segment) *big.Rat {
	if seg.Contains(u.At) {
		return new(big.Rat).Set(u.Qty)
	}
	return new(big.Rat)
}

func (u Instant) span() (time.Time, time.Time) { return u.At, u.At }

// Linear is a quantity consumed uniformly over the half-open span
// [Start, End); each overlapping segment receives a share proportional
// to its overlap duration.
type Linear struct {
	Start time.Time
	End   time.Time
	Qty   *big.Rat
}

// NewLinear builds a Linear usage, rejecting zero or negative durations.
func NewLinear(start, end time.Time, qty *big.Rat) (Linear, error) {
	if !end.After(start) {
		return Linear{}, errors.New("settle: linear usage requires end after start")
	}
	if qty == nil {
		return Linear{}, errors.New("settle: linear usage requires non-nil quantity")
	}
	return Linear{Start: start, End: end, Qty: qty}, nil
}

func (u Linear) allocate(seg Segment) *big.Rat {
	over := seg.overlap(u.Start, u.End)
	if over <= 0 {
		return new(big.Rat)
	}
	total := u.End.Sub(u.Start)
	share := new(big.Rat).SetFrac(big.NewInt(int64(over)), big.NewInt(int64(total)))
	return share.Mul(share, u.Qty)
}

func (u Linear) span() (time.Time, time.Time) { return u.Start, u.End }
