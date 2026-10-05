package taxcalc

import (
	"math/big"
	"sort"
)

// Interval is a closed range of gross amounts in cents, [Lo, Hi].
// Hi may be Inf when the range is unbounded.
type Interval struct {
	Lo int64
	Hi int64
}

// InverseNet returns every gross interval whose after-tax amount equals
// target, as a sorted list of disjoint closed intervals. An empty result
// means target is unreachable.
//
// The net function is not assumed to be strictly increasing (or monotone
// at all): the solver works bracket by bracket and solves the rounding
// equation exactly with integer arithmetic, so plateaus, zero-tax
// segments, jumps and deduction cliffs are all handled. The rules must be
// valid (see Validate).
func (r Rules) InverseNet(target int64) []Interval {
	cum := big.NewInt(0) // rounded tax of all fully completed brackets
	var out []Interval
	for i, b := range r.Brackets {
		out = append(out, r.inverseBracket(target, i, cum)...)
		if b.Upper != Inf {
			width := big.NewInt(b.Upper - b.Lower)
			cum.Add(cum, roundRate(b.Rate, width, r.Mode))
		}
	}
	return mergeIntervals(out)
}

// inverseBracket finds all gross amounts inside bracket idx whose net
// equals target. cumBefore is the rounded tax of brackets below idx.
//
// For a gross x = L + t inside the bracket, with S = cumBefore - deduction:
//
//	net(x) = x - max(0, S + round(rate * t))
//
// Two phases are solved separately:
//
//	Phase A (zero tax): S + round(rate*t) <= 0, so net == x and only the
//	single point x == target can match.
//
//	Phase B (positive tax): net == target requires
//	round(rate*t) = t + d with d = L - target - S, a linear integer
//	feasibility problem solved by solveRoundEq. Note that when the
//	equation holds, the tax equals x - target, so the positivity
//	requirement is simply x >= target + 1.
func (r Rules) inverseBracket(target int64, idx int, cumBefore *big.Int) []Interval {
	b := r.Brackets[idx]
	L := big.NewInt(b.Lower)
	tgt := big.NewInt(target)
	S := new(big.Int).Sub(cumBefore, big.NewInt(b.Deduction))

	var res []Interval

	// Phase A: zero-tax point.
	if b.Lower <= target && (b.Upper == Inf || target < b.Upper) {
		t := new(big.Int).Sub(tgt, L)
		g := new(big.Int).Add(S, roundRate(b.Rate, t, r.Mode))
		if g.Sign() <= 0 {
			res = append(res, Interval{Lo: target, Hi: target})
		}
	}

	// Phase B: positive tax, solve round(rate*t) = t + d.
	d := new(big.Int).Sub(L, tgt)
	d.Sub(d, S)
	tLo := new(big.Int).Sub(tgt, L)
	tLo.Add(tLo, big.NewInt(1)) // x >= target + 1
	if tLo.Sign() < 0 {
		tLo.SetInt64(0)
	}
	var tHi *big.Int // nil means unbounded
	if b.Upper != Inf {
		tHi = big.NewInt(b.Upper - 1 - b.Lower)
	}

	lo, hi, ok := solveRoundEq(b.Rate, d, r.Mode)
	if ok {
		lo = maxBig(lo, tLo)
		hi = minBig(hi, tHi)
		if lo != nil && hi != nil && lo.Cmp(hi) > 0 {
			ok = false
		}
	}
	if ok {
		if lo == nil {
			lo = big.NewInt(0) // unreachable: tLo >= 0 keeps lo bounded
		}
		xLo := clampInt64(new(big.Int).Add(L, lo))
		xHi := Inf
		if hi != nil {
			xHi = clampInt64(new(big.Int).Add(L, hi))
		}
		res = append(res, Interval{Lo: xLo, Hi: xHi})
	}
	return res
}

// solveRoundEq solves round(rate * t) = t + d for integer t >= 0, where
// rate = p/q. It returns closed bounds on t; a nil bound means unbounded
// on that side. ok is false when no t satisfies the equation.
//
// Each rounding mode turns the equation into two linear inequalities in t
// with exact integer coefficients (m stands for t + d):
//
//	half-up: (2m-1)q <= 2pt < (2m+1)q
//	floor:        mq <=  pt < (m+1)q
//	ceil:    (m-1)q <    pt <= mq     <=>  (m-1)q + 1 <= pt <= mq
func solveRoundEq(rate Rate, d *big.Int, mode RoundMode) (lo, hi *big.Int, ok bool) {
	p := big.NewInt(rate.Num)
	q := big.NewInt(rate.Den)
	one := big.NewInt(1)
	two := big.NewInt(2)

	var P, a1, b1, a2, b2 *big.Int // a1*t + b1 <= P*t, P*t </<= a2*t + b2
	rightStrict := true
	switch mode {
	case RoundDown:
		P, a1, a2 = p, q, q
		b1 = new(big.Int).Mul(d, q)
		b2 = new(big.Int).Mul(new(big.Int).Add(d, one), q)
	case RoundUp:
		P, a1, a2 = p, q, q
		b1 = new(big.Int).Mul(new(big.Int).Sub(d, one), q)
		b1.Add(b1, one)
		b2 = new(big.Int).Mul(d, q)
		rightStrict = false
	default: // RoundHalfUp
		P = new(big.Int).Mul(p, two)
		a1 = new(big.Int).Mul(q, two)
		a2 = new(big.Int).Mul(q, two)
		b1 = new(big.Int).Mul(new(big.Int).Sub(new(big.Int).Mul(d, two), one), q)
		b2 = new(big.Int).Mul(new(big.Int).Add(new(big.Int).Mul(d, two), one), q)
	}

	lo1, hi1, ok1 := boundGe(new(big.Int).Sub(P, a1), b1)
	var lo2, hi2 *big.Int
	var ok2 bool
	diff := new(big.Int).Sub(P, a2)
	if rightStrict {
		lo2, hi2, ok2 = boundLt(diff, b2)
	} else {
		lo2, hi2, ok2 = boundLe(diff, b2)
	}
	if !ok1 || !ok2 {
		return nil, nil, false
	}
	lo = maxBig(lo1, lo2)
	hi = minBig(hi1, hi2)
	if lo != nil && hi != nil && lo.Cmp(hi) > 0 {
		return nil, nil, false
	}
	return lo, hi, true
}

// boundGe solves A*t >= B for integer t. nil bounds are unbounded.
func boundGe(A, B *big.Int) (lo, hi *big.Int, ok bool) {
	switch A.Sign() {
	case 0:
		return nil, nil, B.Sign() <= 0
	case 1:
		return ceilDiv(B, A), nil, true
	default:
		return nil, floorDiv(B, A), true
	}
}

// boundLt solves A*t < B for integer t.
func boundLt(A, B *big.Int) (lo, hi *big.Int, ok bool) {
	switch A.Sign() {
	case 0:
		return nil, nil, B.Sign() > 0
	case 1:
		return nil, new(big.Int).Sub(ceilDiv(B, A), big.NewInt(1)), true
	default:
		return new(big.Int).Add(floorDiv(B, A), big.NewInt(1)), nil, true
	}
}

// boundLe solves A*t <= B for integer t.
func boundLe(A, B *big.Int) (lo, hi *big.Int, ok bool) {
	switch A.Sign() {
	case 0:
		return nil, nil, B.Sign() >= 0
	case 1:
		return nil, floorDiv(B, A), true
	default:
		return ceilDiv(B, A), nil, true
	}
}

// floorDiv returns floor(a / b) for b != 0.
func floorDiv(a, b *big.Int) *big.Int {
	q := new(big.Int).Quo(a, b) // truncated toward zero
	r := new(big.Int).Rem(a, b)
	if r.Sign() != 0 && (a.Sign() < 0) != (b.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// ceilDiv returns ceil(a / b) for b != 0.
func ceilDiv(a, b *big.Int) *big.Int {
	q := floorDiv(new(big.Int).Neg(a), b)
	return q.Neg(q)
}

// maxBig treats nil as -inf, minBig treats nil as +inf.
func maxBig(a, b *big.Int) *big.Int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) < 0 {
		return b
	}
	return a
}

func minBig(a, b *big.Int) *big.Int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) > 0 {
		return b
	}
	return a
}

func clampInt64(v *big.Int) int64 {
	if !v.IsInt64() {
		if v.Sign() < 0 {
			return -Inf - 1
		}
		return Inf
	}
	return v.Int64()
}

// mergeIntervals sorts intervals and merges overlapping or adjacent ones.
func mergeIntervals(in []Interval) []Interval {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Lo < in[j].Lo })
	out := []Interval{in[0]}
	for _, iv := range in[1:] {
		last := &out[len(out)-1]
		if last.Hi == Inf || iv.Lo <= last.Hi+1 {
			if iv.Hi == Inf || iv.Hi > last.Hi {
				last.Hi = iv.Hi
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
