package trigcert

import "math/big"

// atanInv returns a certified enclosure of atan(1/q) for an integer q >= 2
// using n terms of the Taylor series
//
//	atan(1/q) = sum_{i>=0} (-1)^i / ((2i+1) q^(2i+1)).
//
// The series is alternating with terms decreasing in absolute value, so the
// absolute error of the n-term partial sum is bounded by the first omitted
// term 1/((2n+1) q^(2n+1)). The returned interval is [S - r, S + r] with S
// the partial sum and r that bound; it is guaranteed to contain atan(1/q).
func atanInv(q int64, n int) Interval {
	if n < 1 {
		n = 1
	}
	sum := new(big.Rat)
	term := new(big.Rat).SetInt64(1)
	term.Quo(term, new(big.Rat).SetInt64(q)) // 1/q
	q2 := new(big.Rat).SetInt64(q * q)
	sign := int64(1)
	for i := 0; i < n; i++ {
		t := new(big.Rat).Set(term)
		t.Quo(t, new(big.Rat).SetInt64(int64(2*i+1)))
		if sign > 0 {
			sum.Add(sum, t)
		} else {
			sum.Sub(sum, t)
		}
		sign = -sign
		term.Quo(term, q2)
	}
	// term is now 1/q^(2n+1); the remainder bound is term/(2n+1).
	rem := new(big.Rat).Quo(term, new(big.Rat).SetInt64(int64(2*n+1)))
	return Interval{
		Lo: new(big.Rat).Sub(sum, rem),
		Hi: new(big.Rat).Add(sum, rem),
	}
}

// PiInterval returns a certified enclosure of pi computed from Machin's
// formula
//
//	pi = 16*atan(1/5) - 4*atan(1/239)
//
// using n series terms for each arctangent. Every operation is exact
// rational arithmetic and every truncation carries an explicit remainder
// bound, so the result is guaranteed to contain pi. No floating-point
// approximation of pi is used anywhere.
func PiInterval(n int) Interval {
	a := atanInv(5, n)
	b := atanInv(239, n)
	pi := a.Scale(big.NewRat(16, 1))
	return pi.Sub(b.Scale(big.NewRat(4, 1)))
}
