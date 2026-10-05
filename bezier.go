package main

import "math/big"

// Curve is a 1D rational Bezier curve: control values and positive weights.
type Curve struct {
	Values  []*big.Rat
	Weights []*big.Rat
}

func (c Curve) Deg() int { return len(c.Values) - 1 }

func binomial(n, k int) *big.Int {
	r := big.NewInt(1)
	for i := 0; i < k; i++ {
		r.Mul(r, big.NewInt(int64(n-i)))
		r.Quo(r, big.NewInt(int64(i+1)))
	}
	return r
}

// numDen builds numerator N(t) = sum w_i b_i B_i^n(t) and denominator
// D(t) = sum w_i B_i^n(t) as polynomials in the power basis.
func (c Curve) numDen() (Poly, Poly) {
	n := c.Deg()
	N, D := Poly{}, Poly{}
	for i := 0; i <= n; i++ {
		// B_i^n(t) = C(n,i) t^i (1-t)^(n-i)
		basis := Poly{ratI(1)}
		ut := Poly{ratI(1), ratI(-1)} // (1 - t)
		for j := 0; j < n-i; j++ {
			basis = mulP(basis, ut)
		}
		shifted := make(Poly, i+len(basis))
		for k := range shifted {
			shifted[k] = ratI(0)
		}
		copy(shifted[i:], basis)
		basis = norm(shifted)
		bin := new(big.Rat).SetInt(binomial(n, i))
		wd := new(big.Rat).Mul(bin, c.Weights[i])
		D = addP(D, scaleP(basis, wd))
		wn := new(big.Rat).Mul(wd, c.Values[i])
		N = addP(N, scaleP(basis, wn))
	}
	return N, D
}

// derivNumerator returns P = N' D - N D' with gcd(N, D) factored out first.
// sign(P) = sign(B') because B' = P / D^2 and D > 0 on [0,1].
func (c Curve) derivNumerator() (P, N, D Poly) {
	N, D = c.numDen()
	if g := gcdP(N, D); g.Deg() >= 1 {
		N, _ = divModP(N, g)
		D, _ = divModP(D, g)
	}
	P = subP(mulP(derivP(N), D), mulP(N, derivP(D)))
	return P, N, D
}

// eval evaluates B(x) exactly at a rational point.
func (c Curve) eval(x *big.Rat) *big.Rat {
	N, D := c.numDen()
	nv, dv := N.eval(x), D.eval(x)
	return new(big.Rat).Quo(nv, dv)
}

// --- exact rational interval arithmetic ---

type iv struct{ L, R *big.Rat }

func addIv(a, b iv) iv {
	return iv{new(big.Rat).Add(a.L, b.L), new(big.Rat).Add(a.R, b.R)}
}

func mulIv(a, b iv) iv {
	prods := []*big.Rat{
		new(big.Rat).Mul(a.L, b.L), new(big.Rat).Mul(a.L, b.R),
		new(big.Rat).Mul(a.R, b.L), new(big.Rat).Mul(a.R, b.R),
	}
	lo, hi := prods[0], prods[0]
	for _, p := range prods[1:] {
		if p.Cmp(lo) < 0 {
			lo = p
		}
		if p.Cmp(hi) > 0 {
			hi = p
		}
	}
	return iv{lo, hi}
}

// evalPolyIv evaluates p over an interval by Horner with interval arithmetic.
func evalPolyIv(p Poly, x iv) iv {
	if p.Zero() {
		return iv{ratI(0), ratI(0)}
	}
	r := iv{new(big.Rat).Set(p.LC()), new(big.Rat).Set(p.LC())}
	for i := len(p) - 2; i >= 0; i-- {
		r = mulIv(r, x)
		r = addIv(r, iv{new(big.Rat).Set(p.Get(i)), new(big.Rat).Set(p.Get(i))})
	}
	return r
}
