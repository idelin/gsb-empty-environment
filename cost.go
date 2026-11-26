package joinenum

import (
	"math"
	"math/bits"
)

// CostModel estimates one binary join.  leftCard and rightCard are the exact
// cardinalities of the two inputs, kind is the operator, and sel is the
// predicate selectivity.  It returns the output cardinality and the local
// execution cost.
//
// Implementations must be deterministic and must not use floating point if
// the exact-integer contract is required.
type CostModel interface {
	Join(kind OpKind, leftCard, rightCard int64, sel Selectivity) (outCard, localCost int64, err error)
}

// CostFunc adapts a function to CostModel.
type CostFunc func(kind OpKind, leftCard, rightCard int64, sel Selectivity) (outCard, localCost int64, err error)

// Join implements CostModel.
func (f CostFunc) Join(kind OpKind, leftCard, rightCard int64, sel Selectivity) (int64, int64, error) {
	return f(kind, leftCard, rightCard, sel)
}

// DefaultCostModel is a deterministic nested-loop cost model:
//
//	pairs        = |L| * |R|
//	matchedPairs = round(pairs * sel)
//	matchedLeft  = min(|L|, round(|L| * sel))
//	InnerJoin    : out = matchedPairs
//	LeftOuterJoin: out = matchedPairs + (|L| - matchedLeft)
//	SemiJoin     : out = matchedLeft
//	AntiJoin     : out = |L| - matchedLeft
//	localCost    = pairs
//
// Rounding is half-up on exact integer rationals.  Total plan cost is the sum
// of local costs.  When the right input is empty all match counts are zero, so
// a left outer join emits exactly its left input and an anti join emits its
// whole left input.
type DefaultCostModel struct{}

// Join implements CostModel.
func (DefaultCostModel) Join(kind OpKind, leftCard, rightCard int64, sel Selectivity) (int64, int64, error) {
	if leftCard < 0 || rightCard < 0 {
		return 0, 0, ErrInvalidInput
	}
	pairs, ok := checkedMul(leftCard, rightCard)
	if !ok {
		return 0, 0, ErrCardinalityOverflow
	}
	matchedPairs, ok := mulRatio(pairs, sel)
	if !ok {
		return 0, 0, ErrCardinalityOverflow
	}
	matchedLeft, ok := mulRatio(leftCard, sel)
	if !ok {
		return 0, 0, ErrCardinalityOverflow
	}
	if rightCard == 0 {
		matchedPairs = 0
		matchedLeft = 0
	}
	if matchedLeft > leftCard {
		matchedLeft = leftCard
	}
	var out int64
	switch kind {
	case InnerJoin:
		out = matchedPairs
	case LeftOuterJoin:
		var ok bool
		out, ok = checkedAdd(matchedPairs, leftCard-matchedLeft)
		if !ok {
			return 0, 0, ErrCardinalityOverflow
		}
	case SemiJoin:
		out = matchedLeft
	case AntiJoin:
		out = leftCard - matchedLeft
	default:
		return 0, 0, ErrInvalidInput
	}
	if out < 0 {
		return 0, 0, ErrCardinalityOverflow
	}
	return out, pairs, nil
}

func checkedMul(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > math.MaxInt64/b {
		return 0, false
	}
	return a * b, true
}

// mulRatio returns round(v * num/den) with half-up rounding.
func mulRatio(v int64, s Selectivity) (int64, bool) {
	if v < 0 || s.Den <= 0 || s.Num < 0 {
		return 0, false
	}
	if v == 0 || s.Num == 0 {
		return 0, true
	}
	// v*Num may overflow; use 128-bit arithmetic.
	hi, lo := bits.Mul64(uint64(v), uint64(s.Num))
	den := uint64(s.Den)
	if hi >= den {
		return 0, false
	}
	q, r := bits.Div64(hi, lo, den)
	if q > math.MaxInt64 {
		return 0, false
	}
	// half-up: r/den >= 1/2
	if r >= (den+1)/2 {
		q++
	}
	if q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}
