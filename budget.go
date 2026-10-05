// Package bigsqrt computes integer square roots of non-negative
// big integers with streaming decimal input, an operation budget
// counted in big-integer operations, and cancel/resume support.
package bigsqrt

import "errors"

// ErrBudgetExhausted is returned when an operation would exceed the
// remaining big-integer operation budget. The computation can be
// resumed later with a fresh budget.
var ErrBudgetExhausted = errors.New("bigsqrt: big-integer operation budget exhausted")

// Budget tracks a remaining allowance of big-integer operations.
// A nil *Budget means "unlimited" and is accepted everywhere a
// budget is taken.
type Budget struct {
	remaining int64
}

// NewBudget returns a Budget allowing ops big-integer operations.
func NewBudget(ops int64) *Budget {
	if ops < 0 {
		ops = 0
	}
	return &Budget{remaining: ops}
}

// Remaining reports how many operations are left.
func (b *Budget) Remaining() int64 {
	if b == nil {
		return -1 // unlimited
	}
	return b.remaining
}

// charge consumes n operations, or fails without consuming anything
// when fewer than n operations remain.
func (b *Budget) charge(n int64) error {
	if b == nil {
		return nil
	}
	if b.remaining < n {
		return ErrBudgetExhausted
	}
	b.remaining -= n
	return nil
}
