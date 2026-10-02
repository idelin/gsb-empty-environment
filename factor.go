package algebraic

import "math/big"

// sqfrFactors returns the distinct square-free, pairwise coprime factors
// of f (multiplicities dropped), each monic.
func sqfrFactors(f Poly) []Poly {
	if f.Deg() <= 0 {
		return nil
	}
	f = f.Monic()
	g := PolyGCD(f, f.Deriv())
	w, _ := f.QuoRem(g)
	var res []Poly
	for !w.IsOne() {
		y := PolyGCD(w, g)
		z, _ := w.QuoRem(y)
		if !z.IsOne() {
			res = append(res, z.Monic())
		}
		w = y
		g, _ = g.QuoRem(y)
	}
	return res
}

// Factor factors f into irreducible monic polynomials over Q.
func Factor(f Poly) []Poly {
	var out []Poly
	for _, sf := range sqfrFactors(f) {
		ip := toIntPrimitive(sf)
		for _, g := range kroneckerFactor(ip) {
			out = append(out, polyFromInts(g).Monic())
		}
	}
	return out
}

// toIntPrimitive clears denominators and divides out the content,
// returning a primitive integer polynomial with positive leading
// coefficient.
func toIntPrimitive(p Poly) []*big.Int {
	lcm := big.NewInt(1)
	for _, c := range p.c {
		d := c.Denom()
		g := new(big.Int).GCD(nil, nil, new(big.Int).Set(lcm), d)
		lcm.Quo(lcm, g)
		lcm.Mul(lcm, d)
	}
	coeffs := make([]*big.Int, len(p.c))
	for i, c := range p.c {
		coeffs[i] = new(big.Int).Mul(c.Num(), new(big.Int).Quo(new(big.Int).Set(lcm), c.Denom()))
	}
	return makePrimitive(coeffs)
}

func makePrimitive(c []*big.Int) []*big.Int {
	g := new(big.Int)
	for _, k := range c {
		g.GCD(nil, nil, g, new(big.Int).Abs(k))
	}
	if g.Sign() == 0 {
		g.SetInt64(1)
	}
	out := make([]*big.Int, len(c))
	for i, k := range c {
		out[i] = new(big.Int).Quo(new(big.Int).Set(k), g)
	}
	if out[len(out)-1].Sign() < 0 {
		for i := range out {
			out[i].Neg(out[i])
		}
	}
	return out
}

func polyFromInts(c []*big.Int) Poly {
	r := make([]*big.Rat, len(c))
	for i, k := range c {
		r[i] = new(big.Rat).SetInt(k)
	}
	return polyFromCoeffs(r)
}

func toIntIfIntegral(p Poly) ([]*big.Int, bool) {
	c := make([]*big.Int, len(p.c))
	for i, cf := range p.c {
		if cf.Denom().Cmp(big.NewInt(1)) != 0 {
			return nil, false
		}
		c[i] = new(big.Int).Set(cf.Num())
	}
	return c, true
}

func evalIntPoly(f []*big.Int, x int64) *big.Int {
	r := new(big.Int)
	bx := big.NewInt(x)
	for i := len(f) - 1; i >= 0; i-- {
		r.Mul(r, bx)
		r.Add(r, f[i])
	}
	return r
}

func signedDivisors(n *big.Int) []*big.Int {
	a := new(big.Int).Abs(n)
	var divs []*big.Int
	one := big.NewInt(1)
	sqrt := new(big.Int).Sqrt(a)
	for d := big.NewInt(1); d.Cmp(sqrt) <= 0; d.Add(d, one) {
		if new(big.Int).Mod(a, d).Sign() == 0 {
			e := new(big.Int).Quo(new(big.Int).Set(a), d)
			divs = append(divs, new(big.Int).Set(d), new(big.Int).Neg(new(big.Int).Set(d)))
			if e.Cmp(d) != 0 {
				divs = append(divs, new(big.Int).Set(e), new(big.Int).Neg(new(big.Int).Set(e)))
			}
		}
	}
	return divs
}

func dividesExactly(f, g []*big.Int) bool {
	_, r := polyFromInts(f).QuoRem(polyFromInts(g))
	return r.IsZero()
}

func exactQuotient(f, g []*big.Int) []*big.Int {
	q, _ := polyFromInts(f).QuoRem(polyFromInts(g))
	return toIntPrimitive(q)
}

// kroneckerFactor factors a primitive square-free integer polynomial into
// irreducible integer polynomials using Kronecker's algorithm.
func kroneckerFactor(f []*big.Int) [][]*big.Int {
	if len(f) <= 2 {
		return [][]*big.Int{f}
	}
	if irreducibleOverQ(f) {
		return [][]*big.Int{f}
	}
	n := len(f) - 1
	for d := 1; d <= n/2; d++ {
		if g, ok := kroneckerFindFactor(f, d); ok {
			q := exactQuotient(f, g)
			left := kroneckerFactor(g)
			right := kroneckerFactor(q)
			return append(left, right...)
		}
	}
	return [][]*big.Int{f}
}

// kroneckerFindFactor looks for a degree-d factor of f by interpolating
// divisor tuples of f's values at d+1 integer points.
func kroneckerFindFactor(f []*big.Int, d int) ([]*big.Int, bool) {
	var xs []int64
	var vs []*big.Int
	for x := int64(0); len(xs) < d+1; x = nextX(x) {
		v := evalIntPoly(f, x)
		if v.Sign() == 0 {
			// x is an integer root: x - x0 is a factor.
			return []*big.Int{big.NewInt(-x), big.NewInt(1)}, true
		}
		xs = append(xs, x)
		vs = append(vs, v)
	}
	divs := make([][]*big.Int, len(xs))
	for i, v := range vs {
		divs[i] = signedDivisors(v)
	}
	xrats := make([]*big.Rat, len(xs))
	for i, x := range xs {
		xrats[i] = big.NewRat(x, 1)
	}
	ys := make([]*big.Rat, len(xs))
	idx := make([]int, len(xs))
	for {
		for i := range ys {
			ys[i] = new(big.Rat).SetInt(divs[i][idx[i]])
		}
		g := interpolate(xrats, ys)
		if g.Deg() == d {
			if gi, ok := toIntIfIntegral(g); ok {
				gi = makePrimitive(gi)
				if dividesExactly(f, gi) {
					return gi, true
				}
			}
		}
		k := 0
		for {
			idx[k]++
			if idx[k] < len(divs[k]) {
				break
			}
			idx[k] = 0
			k++
			if k == len(idx) {
				return nil, false
			}
		}
	}
}

// nextX enumerates 0, 1, -1, 2, -2, ...
func nextX(x int64) int64 {
	if x > 0 {
		return -x
	}
	return 1 - x
}
