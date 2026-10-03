package ratbezier

import "math/big"

// interval is a closed rational interval isolating exactly one real root.
type interval struct {
	lo, hi *big.Rat
}

func (iv interval) approx() *big.Rat {
	return new(big.Rat).Quo(new(big.Rat).Add(iv.lo, iv.hi), big.NewRat(2, 1))
}

// isolateRoots isolates every distinct real root of p in (a, b] into a
// disjoint rational interval of width at most eps. Roots exactly at a are
// not reported; callers extract rational roots (including endpoints)
// beforehand, so this is used on polynomials without rational roots.
func isolateRoots(p *Poly, a, b, eps *big.Rat) []interval {
	if p.Degree() < 1 {
		return nil
	}
	seq := buildSturmSeq(p)
	two := big.NewRat(2, 1)
	var out []interval
	var rec func(lo, hi *big.Rat)
	rec = func(lo, hi *big.Rat) {
		n := seq.countRoots(lo, hi)
		if n == 0 {
			return
		}
		if n == 1 && new(big.Rat).Sub(hi, lo).Cmp(eps) <= 0 {
			out = append(out, interval{new(big.Rat).Set(lo), new(big.Rat).Set(hi)})
			return
		}
		mid := new(big.Rat).Add(lo, hi)
		mid.Quo(mid, two)
		rec(lo, mid)
		rec(mid, hi)
	}
	rec(a, b)
	return out
}

// extractRationalRoots repeatedly pulls out every rational root of p
// (with multiplicity) using the rational root theorem, returning the
// roots found and the remaining cofactor, which has no rational roots.
func extractRationalRoots(p *Poly) ([]*big.Rat, *Poly) {
	var roots []*big.Rat
	rest := polyFromBig(p.Coeffs())
	for rest.Degree() >= 1 {
		r, ok := findRationalRoot(rest)
		if !ok {
			break
		}
		roots = append(roots, r)
		rest = divideByLinearFactor(rest, r)
	}
	return roots, rest
}

// findRationalRoot searches for one rational root via the rational root
// theorem: any root p/q in lowest terms satisfies p | a0 and q | an.
func findRationalRoot(p *Poly) (*big.Rat, bool) {
	if p.Degree() < 1 {
		return nil, false
	}
	a0 := p.Coeff(0)
	if a0.Sign() == 0 {
		return big.NewRat(0, 1), true
	}
	an := p.Coeff(p.Degree())
	seen := make(map[string]bool)
	for _, num := range divisors(a0) {
		for _, den := range divisors(an) {
			for _, s := range []int64{1, -1} {
				n := new(big.Int).Mul(num, big.NewInt(s))
				r := new(big.Rat).SetFrac(n, den)
				key := r.RatString()
				if seen[key] {
					continue
				}
				seen[key] = true
				if p.EvalRat(r).Sign() == 0 {
					return r, true
				}
			}
		}
	}
	return nil, false
}

// divisors returns the positive divisors of n (using |n|).
func divisors(n *big.Int) []*big.Int {
	a := new(big.Int).Abs(n)
	if a.Sign() == 0 {
		return []*big.Int{big.NewInt(1)}
	}
	var out []*big.Int
	one := big.NewInt(1)
	sqrt := new(big.Int).Sqrt(a)
	for i := big.NewInt(1); i.Cmp(sqrt) <= 0; i.Add(i, one) {
		rem := new(big.Int)
		q := new(big.Int)
		q.QuoRem(a, i, rem)
		if rem.Sign() == 0 {
			out = append(out, new(big.Int).Set(i))
			if q.Cmp(i) != 0 {
				out = append(out, q)
			}
		}
	}
	return out
}

// divideByLinearFactor divides p by (den*x - num) where r = num/den is a
// root of p in lowest terms. The quotient stays in Z[x] because the
// linear factor is primitive.
func divideByLinearFactor(p *Poly, r *big.Rat) *Poly {
	num := r.Num()
	den := r.Denom()
	n := p.Degree()
	b := make([]*big.Int, n)
	// a_k = den*b_{k-1} - num*b_k, solved top-down.
	b[n-1] = new(big.Int).Quo(p.Coeff(n), den)
	for k := n - 1; k >= 1; k-- {
		t := new(big.Int).Add(p.Coeff(k), new(big.Int).Mul(num, b[k]))
		b[k-1] = new(big.Int).Quo(t, den)
	}
	return polyFromBig(b)
}
