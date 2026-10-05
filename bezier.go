package ratbez

import "math/big"

func binomial(n, k int) *big.Rat {
	r := big.NewRat(1, 1)
	for i := 0; i < k; i++ {
		r.Mul(r, big.NewRat(int64(n-i), 1))
		r.Quo(r, big.NewRat(int64(i+1), 1))
	}
	return r
}

// bernstein returns the i-th Bernstein basis polynomial of degree n:
// B_i^n(t) = C(n,i) t^i (1-t)^(n-i).
func bernstein(n, i int) Poly {
	p := PolyInts(1)
	oneMinusT := PolyInts(1, -1)
	for k := 0; k < n-i; k++ {
		p = Mul(p, oneMinusT)
	}
	// multiply by t^i (shift coefficients)
	c := make([]*big.Rat, i+len(p.c))
	for j := range c {
		c[j] = new(big.Rat)
	}
	copy(c[i:], p.c)
	p = normalize(c)
	return Scale(p, binomial(n, i))
}

// buildND builds the numerator and denominator polynomials of the rational
// Bezier curve R(t) = N(t)/D(t) with control values P and weights W:
//
//	N(t) = sum_i W_i P_i B_i^n(t)
//	D(t) = sum_i W_i B_i^n(t)
func buildND(P, W []*big.Rat) (N, D Poly) {
	n := len(P) - 1
	for i := 0; i <= n; i++ {
		B := bernstein(n, i)
		N = Add(N, Scale(B, new(big.Rat).Mul(W[i], P[i])))
		D = Add(D, Scale(B, W[i]))
	}
	return
}
