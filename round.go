package interval

import (
	"math/big"
	"strings"
)

func scaleInt(digits int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
}

// FloorTo rounds r down (toward -inf) to a multiple of 10^-digits.
func FloorTo(r *big.Rat, digits int) *big.Rat {
	scale := scaleInt(digits)
	num := new(big.Int).Mul(r.Num(), scale)
	den := r.Denom()
	q := new(big.Int).Quo(num, den) // truncated toward zero
	if new(big.Int).Rem(num, den).Sign() != 0 && num.Sign() < 0 {
		q.Sub(q, big.NewInt(1)) // floor for negative values
	}
	return new(big.Rat).SetFrac(q, scale)
}

// CeilTo rounds r up (toward +inf) to a multiple of 10^-digits.
func CeilTo(r *big.Rat, digits int) *big.Rat {
	return new(big.Rat).Neg(FloorTo(new(big.Rat).Neg(r), digits))
}

// Enclose rounds the endpoints outward to decimals with the given
// number of fraction digits: the left endpoint is rounded down, the
// right endpoint up, so the result always contains i. Openness flags
// are preserved, which never breaks containment: a rounded-down left
// endpoint stays <= the exact one, and a rounded-up right endpoint
// stays >= the exact one. Infinite endpoints are kept. The empty set
// stays empty; a single point maps to a non-empty decimal enclosure.
func (i Interval) Enclose(digits int) Interval {
	if i.empty {
		return i
	}
	lo, hi := i.lo, i.hi
	if lo.inf == 0 {
		lo = Fin(FloorTo(lo.val, digits))
	}
	if hi.inf == 0 {
		hi = Fin(CeilTo(hi.val, digits))
	}
	return Make(lo, hi, i.loOpen, i.hiOpen)
}

// Enclose rounds every segment outward; rounding can only expand
// segments, so the result still contains s. Segments that start to
// overlap after rounding are merged, and MaxSegments is re-enforced
// conservatively.
func (s *Set) Enclose(digits int) *Set {
	out := NewSet(s.MaxSegments)
	for _, iv := range s.Segments {
		out.Add(iv.Enclose(digits))
	}
	return out
}

// DecimalString formats r with exactly digits fraction digits. r must
// already be a multiple of 10^-digits (as produced by FloorTo/CeilTo).
func DecimalString(r *big.Rat, digits int) string {
	n := new(big.Int).Mul(r.Num(), scaleInt(digits))
	n.Quo(n, r.Denom()) // exact by construction
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
		n.Abs(n)
	}
	s := n.String()
	if digits == 0 {
		return sign + s
	}
	if len(s) <= digits {
		s = strings.Repeat("0", digits-len(s)+1) + s
	}
	return sign + s[:len(s)-digits] + "." + s[len(s)-digits:]
}
