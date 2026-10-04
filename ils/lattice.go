package ils

import "math/big"

// gcdInt is a zero-safe greatest common divisor (the result is always
// non-negative). It works around big.Int.GCD returning 0 when one argument
// is zero on older Go versions.
func gcdInt(a, b *big.Int) *big.Int {
	aa := new(big.Int).Abs(a)
	bb := new(big.Int).Abs(b)
	if aa.Sign() == 0 {
		return bb
	}
	if bb.Sign() == 0 {
		return aa
	}
	return aa.GCD(nil, nil, aa, bb)
}

// det computes the determinant of the s x s matrix formed by the given rows
// and column indices, using the fraction-free Bareiss algorithm.
func det(rows [][]*big.Int, cols []int) *big.Int {
	s := len(cols)
	a := make([][]*big.Int, s)
	for i := range a {
		a[i] = make([]*big.Int, s)
		for j := range a[i] {
			a[i][j] = new(big.Int).Set(rows[i][cols[j]])
		}
	}
	sign := int64(1)
	prev := big.NewInt(1)
	num, den := new(big.Int), new(big.Int)
	for k := 0; k < s-1; k++ {
		if a[k][k].Sign() == 0 {
			sw := -1
			for i := k + 1; i < s; i++ {
				if a[i][k].Sign() != 0 {
					sw = i
					break
				}
			}
			if sw == -1 {
				return new(big.Int)
			}
			a[k], a[sw] = a[sw], a[k]
			sign = -sign
		}
		for i := k + 1; i < s; i++ {
			for j := k + 1; j < s; j++ {
				num.Mul(a[i][j], a[k][k])
				den.Mul(a[i][k], a[k][j])
				num.Sub(num, den)
				a[i][j].Quo(num, prev)
			}
		}
		prev.Set(a[k][k])
	}
	return a[s-1][s-1].Mul(a[s-1][s-1], big.NewInt(sign))
}

// maximalMinorGCD returns the gcd of all s x s minors of the matrix whose
// rows are the given s vectors of length n. A rank-s family of integer
// vectors spans a saturated sublattice of Z^n (a direct summand) iff this
// gcd is 1; equivalently all Smith invariant factors of the basis matrix
// are 1. The computation stops as soon as the running gcd reaches 1.
func maximalMinorGCD(rows [][]*big.Int, n int) *big.Int {
	s := len(rows)
	g := new(big.Int)
	if s == 0 || s > n {
		return g
	}
	one := big.NewInt(1)
	cols := make([]int, s)
	var rec func(start, k int)
	rec = func(start, k int) {
		if g.Cmp(one) == 0 {
			return
		}
		if k == s {
			g = gcdInt(g, det(rows, cols))
			return
		}
		for c := start; c <= n-(s-k); c++ {
			cols[k] = c
			rec(c+1, k+1)
		}
	}
	rec(0, 0)
	return g
}
