// Package taxcalc implements a synthetic integer-cents tax engine with
// exact forward and inverse computation.
//
// Model (self-contained, not based on any real tax law):
//
//   - A Schedule is an ordered list of contiguous brackets covering
//     [0, +Inf). Each bracket has an exact rational rate Num/Den and a
//     fixed deduction in cents.
//   - For a gross amount inside bracket i, every bracket j < i
//     contributes round(width_j * rate_j) - ded_j, and bracket i
//     contributes round(d * rate_i) - ded_i, where d = gross - lower_i.
//   - Rounding happens per bracket ("at each bracket's end"): each
//     bracket's subtotal is rounded to integer cents independently,
//     using the schedule's RoundingMode. Brackets never share
//     fractional cents.
//   - A bracket's deduction applies in full as soon as gross enters the
//     bracket, so the net (after-tax) function may jump at bracket
//     boundaries. With rate > 1 it may decrease; with rate == 1 it is
//     flat. Callers must not assume net(gross) is strictly increasing.
//
// All amounts are integer cents. Tax results are assumed to fit in
// int64 (intermediate products use big.Int internally).
package taxcalc

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
)

// Inf marks the unbounded upper bound of the top bracket (exclusive).
const Inf = int64(math.MaxInt64)

// RoundingMode selects how a bracket's fractional tax is rounded to
// integer cents. All modes are translation-invariant by integers.
type RoundingMode int

const (
	RoundHalfUp RoundingMode = iota
	RoundHalfEven
	RoundDown // towards zero; amounts are non-negative, so this is floor
	RoundUp   // away from zero; amounts are non-negative, so this is ceil
)

// Rate is an exact rational tax rate Num/Den, e.g. Rate{1, 5} == 20%.
type Rate struct {
	Num int64
	Den int64
}

// Bracket covers gross amounts in [Lower, Upper), in cents.
type Bracket struct {
	Lower int64 // inclusive
	Upper int64 // exclusive; Inf for the (required unbounded) top bracket
	Rate  Rate
	Ded   int64 // cents subtracted from this bracket's rounded tax
}

// Schedule is a validated tax schedule.
type Schedule struct {
	brackets []Bracket
	mode     RoundingMode
}

// Interval is an inclusive range of gross amounts, [Lo, Hi] in cents.
type Interval struct {
	Lo int64
	Hi int64
}

// New validates brackets and returns a Schedule. Brackets must be
// contiguous, start at 0, have positive width, non-negative rational
// rates, and the top bracket must be unbounded (Upper == Inf).
func New(brackets []Bracket, mode RoundingMode) (Schedule, error) {
	if len(brackets) == 0 {
		return Schedule{}, errors.New("taxcalc: schedule needs at least one bracket")
	}
	if mode < RoundHalfUp || mode > RoundUp {
		return Schedule{}, fmt.Errorf("taxcalc: unknown rounding mode %d", int(mode))
	}
	next := int64(0)
	for i, b := range brackets {
		if b.Rate.Den <= 0 {
			return Schedule{}, fmt.Errorf("taxcalc: bracket %d: rate denominator must be > 0", i)
		}
		if b.Rate.Num < 0 {
			return Schedule{}, fmt.Errorf("taxcalc: bracket %d: rate numerator must be >= 0", i)
		}
		if b.Lower != next {
			return Schedule{}, fmt.Errorf("taxcalc: bracket %d: lower bound %d, expected %d (contiguous from 0)", i, b.Lower, next)
		}
		if b.Upper <= b.Lower {
			return Schedule{}, fmt.Errorf("taxcalc: bracket %d: upper bound %d must exceed lower bound %d", i, b.Upper, b.Lower)
		}
		next = b.Upper
	}
	if brackets[len(brackets)-1].Upper != Inf {
		return Schedule{}, errors.New("taxcalc: top bracket must be unbounded (Upper == taxcalc.Inf)")
	}
	cp := make([]Bracket, len(brackets))
	copy(cp, brackets)
	return Schedule{brackets: cp, mode: mode}, nil
}

// roundMul returns round(v * num / den) for v >= 0, num >= 0, den > 0.
// The result is truncated if it does not fit in int64; callers that
// cannot tolerate overflow (Invert) use roundMulBig instead.
func roundMul(v, num, den int64, mode RoundingMode) int64 {
	return roundMulBig(v, num, den, mode).Int64()
}

// roundMulBig is the exact arbitrary-precision version of roundMul.
func roundMulBig(v, num, den int64, mode RoundingMode) *big.Int {
	p := new(big.Int).Mul(big.NewInt(v), big.NewInt(num))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(p, big.NewInt(den), r) // p >= 0, so q >= 0 and 0 <= r < den
	up := false
	switch mode {
	case RoundHalfUp:
		up = new(big.Int).Lsh(r, 1).Cmp(big.NewInt(den)) >= 0
	case RoundHalfEven:
		c := new(big.Int).Lsh(r, 1).Cmp(big.NewInt(den))
		up = c > 0 || (c == 0 && q.Bit(0) == 1)
	case RoundUp:
		up = r.Sign() != 0
	}
	if up {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// locate returns the index of the bracket containing gross.
func (s Schedule) locate(gross int64) int {
	i := sort.Search(len(s.brackets), func(i int) bool { return gross < s.brackets[i].Upper })
	if i == len(s.brackets) {
		return len(s.brackets) - 1
	}
	return i
}

// Tax returns the total tax in cents for a gross amount (gross >= 0).
func (s Schedule) Tax(gross int64) int64 {
	if gross < 0 {
		gross = 0
	}
	i := s.locate(gross)
	var tax int64
	for j := 0; j < i; j++ {
		b := s.brackets[j]
		tax += roundMul(b.Upper-b.Lower, b.Rate.Num, b.Rate.Den, s.mode) - b.Ded
	}
	b := s.brackets[i]
	tax += roundMul(gross-b.Lower, b.Rate.Num, b.Rate.Den, s.mode) - b.Ded
	return tax
}

// Net returns the after-tax amount in cents for a gross amount.
func (s Schedule) Net(gross int64) int64 { return gross - s.Tax(gross) }

// Invert returns every gross interval whose net equals targetNet, as a
// sorted slice of disjoint, non-adjacent inclusive intervals. It makes
// no monotonicity assumption: plateaus, jumps, and decreases in the net
// function are all handled exactly.
func (s Schedule) Invert(targetNet int64) []Interval {
	var res []Interval
	var carried int64 // sum of round(width*rate) - ded over completed brackets
	for _, b := range s.brackets {
		// Gross in [b.Lower, b.Upper-1]; with d = gross - b.Lower:
		//   net = b.Lower + d - carried - round(d*rate) + b.Ded
		// net == targetNet  <=>  round(d*rate) - d == k
		k := b.Lower - carried + b.Ded - targetNet
		w := b.Upper - 1 - b.Lower // max d in this segment
		if lo, hi, ok := solveSegment(b.Rate, w, k, s.mode); ok {
			res = append(res, Interval{Lo: b.Lower + lo, Hi: b.Lower + hi})
		}
		if b.Upper != Inf {
			carried += roundMul(b.Upper-b.Lower, b.Rate.Num, b.Rate.Den, s.mode) - b.Ded
		}
	}
	return merge(res)
}

// solveSegment finds all integer d in [0, w] with g(d) == k, where
// g(d) = round(d*rate) - d. g is monotone: non-decreasing when
// num >= den, non-increasing when num < den, so solutions form a
// single (possibly empty) contiguous interval found by binary search.
func solveSegment(rate Rate, w, k int64, mode RoundingMode) (lo, hi int64, ok bool) {
	diff := rate.Num - rate.Den
	switch {
	case diff == 0: // g is identically 0
		if k != 0 {
			return 0, 0, false
		}
		return 0, w, true
	case diff > 0 && k < 0: // g(0) == 0 and g non-decreasing
		return 0, 0, false
	case diff < 0 && k > 0: // g(0) == 0 and g non-increasing
		return 0, 0, false
	}
	// g(d) = d*diff/den + eps with |eps| <= 1, so any solution needs
	// d <= den*(|k|+1)/|diff|. Cap the search window accordingly;
	// the arithmetic is exact big.Int since k may be near int64 limits.
	if cap := searchCap(rate.Den, k, diff); cap < w {
		w = cap
	}
	bk := big.NewInt(k)
	// g is evaluated in big.Int so that huge rates cannot overflow the
	// search even when the tax itself would exceed int64.
	g := func(d int64) *big.Int {
		r := roundMulBig(d, rate.Num, rate.Den, mode)
		return r.Sub(r, big.NewInt(d))
	}
	if diff > 0 { // non-decreasing
		lo = firstTrue(w, func(d int64) bool { return g(d).Cmp(bk) >= 0 })
		hi = firstTrue(w, func(d int64) bool { return g(d).Cmp(bk) > 0 }) - 1
	} else { // non-increasing
		lo = firstTrue(w, func(d int64) bool { return g(d).Cmp(bk) <= 0 })
		hi = firstTrue(w, func(d int64) bool { return g(d).Cmp(bk) < 0 }) - 1
	}
	if lo > w || hi < 0 || lo > hi || g(lo).Cmp(bk) != 0 {
		return 0, 0, false
	}
	return lo, hi, true
}

// searchCap returns den*(|k|+1)/|diff| + 1 as an int64, saturating at
// math.MaxInt64. Any d beyond this bound cannot solve g(d) == k.
func searchCap(den, k, diff int64) int64 {
	abs := func(v int64) *big.Int {
		b := big.NewInt(v)
		return b.Abs(b)
	}
	cap := abs(k)
	cap.Add(cap, big.NewInt(1))
	cap.Mul(cap, big.NewInt(den))
	cap.Quo(cap, abs(diff))
	cap.Add(cap, big.NewInt(1))
	if !cap.IsInt64() {
		return math.MaxInt64
	}
	return cap.Int64()
}

// firstTrue returns the smallest d in [0, w] satisfying pred, or w+1.
func firstTrue(w int64, pred func(int64) bool) int64 {
	lo, hi := int64(0), w+1 // invariant: pred false below lo, true at hi (sentinel)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if pred(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// merge coalesces overlapping or adjacent intervals. Input must be
// sorted by Lo, which per-segment results already are.
func merge(in []Interval) []Interval {
	out := in[:0]
	for _, iv := range in {
		if n := len(out); n > 0 && iv.Lo <= out[n-1].Hi+1 {
			if iv.Hi > out[n-1].Hi {
				out[n-1].Hi = iv.Hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
