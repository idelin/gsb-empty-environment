// Package circlecover solves the maximum-weight circle covering problem:
// given integer points with non-negative weights and a rational radius,
// find the maximum total weight coverable by a closed disk, together with
// a center attaining it.
//
// All comparisons are exact: coordinates and radii are rational
// (math/big.Rat) and candidate centers are algebraic numbers of the form
// a + b*sqrt(s). No floating-point tolerance is used anywhere.
package circlecover

import "math/big"

// Alg is an algebraic number of the form A + B*sqrt(S), where A, B and S
// are rational and S >= 0. A pure rational is represented with B == 0
// (and then S is irrelevant, conventionally 0).
type Alg struct {
	A, B, S *big.Rat
}

func ratInt(x int64) *big.Rat { return new(big.Rat).SetInt64(x) }

func mulRat(x, y *big.Rat) *big.Rat { return new(big.Rat).Mul(x, y) }

// algInt lifts an integer to a rational-valued Alg.
func algInt(x int64) Alg {
	return Alg{A: ratInt(x), B: new(big.Rat), S: new(big.Rat)}
}

// alg builds A + B*sqrt(S) in canonical form: if the radical part
// vanishes (B == 0 or S == 0) the value is the pure rational A.
func alg(a, b, s *big.Rat) Alg {
	if b.Sign() == 0 || s.Sign() == 0 {
		return Alg{A: a, B: new(big.Rat), S: new(big.Rat)}
	}
	return Alg{A: a, B: b, S: s}
}

// lteqRat reports whether v <= t for a rational t, using exact arithmetic.
//
// v = A + B*sqrt(S). Deciding A + B*sqrt(S) <= t reduces to comparing
// B*sqrt(S) with t - A; when both sides have the same sign this is done
// by squaring, which is exact because every quantity is rational.
func lteqRat(v Alg, t *big.Rat) bool {
	if v.B.Sign() == 0 || v.S.Sign() == 0 {
		return v.A.Cmp(t) <= 0
	}
	diff := new(big.Rat).Sub(t, v.A)
	// lhs2 = B^2 * S, rhs2 = diff^2; both are non-negative rationals.
	lhs2 := new(big.Rat).Mul(v.B, v.B)
	lhs2.Mul(lhs2, v.S)
	rhs2 := new(big.Rat).Mul(diff, diff)
	switch v.B.Sign() {
	case 1:
		// Need B*sqrt(S) <= diff. Impossible if diff < 0; otherwise
		// both sides are non-negative and we may square.
		if diff.Sign() < 0 {
			return false
		}
		return lhs2.Cmp(rhs2) <= 0
	default:
		// B < 0: if diff >= 0 the inequality holds trivially.
		// Otherwise both sides are negative and
		// B*sqrt(S) <= diff  <=>  |B|*sqrt(S) >= |diff|.
		if diff.Sign() >= 0 {
			return true
		}
		return lhs2.Cmp(rhs2) >= 0
	}
}

// String renders the number as "a", "a + b*sqrt(s)" or "a - b*sqrt(s)".
func (a Alg) String() string {
	if a.B.Sign() == 0 || a.S.Sign() == 0 {
		return a.A.RatString()
	}
	sign := " + "
	b := a.B
	if b.Sign() < 0 {
		sign = " - "
		b = new(big.Rat).Abs(b)
	}
	return a.A.RatString() + sign + b.RatString() + "*sqrt(" + a.S.RatString() + ")"
}
