package ratbezier

import (
	"math/big"
)

// Root describes one sign-changing root of a polynomial inside [0,1].
// When Exact is non-nil the root is an exact rational number (Lo == Hi == Exact).
// Otherwise the unique root lies strictly inside the open interval (Lo, Hi),
// whose endpoints are exact rationals.
type Root struct {
	Exact  *big.Rat
	Lo, Hi *big.Rat
}

// IsExact reports whether the root is known as an exact rational number.
func (r Root) IsExact() bool { return r.Exact != nil }

// Approx returns an exact rational representative of the root:
// the root itself when exact, otherwise the interval midpoint.
func (r Root) Approx() *big.Rat {
	if r.Exact != nil {
		return new(big.Rat).Set(r.Exact)
	}
	m := new(big.Rat).Add(r.Lo, r.Hi)
	return m.Mul(m, big.NewRat(1, 2))
}

// refineBits is the number of bisection steps used to tighten isolating
// intervals, giving a width of 2^-refineBits.
const refineBits = 40

// maxDepth bounds the Descartes recursion so that clusters of roots that
// cannot be separated (e.g. irrational even-multiplicity roots) terminate.
const maxDepth = 80

// IsolateRoots finds every sign-changing root of p in the closed interval
// [0,1]. Roots exactly at dyadic rationals (or at 0 and 1) are reported
// exactly; rational roots are recognized exactly via the rational root
// theorem; all remaining roots are enclosed in disjoint rational intervals
// of width at most 2^-refineBits, each containing exactly one root.
func IsolateRoots(p *Poly) []Root {
	if p.IsZero() || p.Degree() <= 0 {
		return nil
	}
	var roots []Root
	zero, one := rat(0), rat(1)
	var rec func(lo, hi *big.Rat, depth int)
	rec = func(lo, hi *big.Rat, depth int) {
		if p.Sign(lo) == 0 {
			roots = append(roots, Root{Exact: new(big.Rat).Set(lo), Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(lo)})
		}
		v := descartesBound(p, lo, hi)
		switch {
		case v == 0:
			return
		case v == 1:
			roots = append(roots, refineRoot(p, lo, hi))
		case depth >= maxDepth:
			roots = append(roots, Root{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)})
		default:
			mid := new(big.Rat).Add(lo, hi)
			mid.Mul(mid, big.NewRat(1, 2))
			rec(lo, mid, depth+1)
			rec(mid, hi, depth+1)
		}
	}
	rec(zero, one, 0)
	if p.Sign(one) == 0 {
		roots = append(roots, Root{Exact: rat(1), Lo: rat(1), Hi: rat(1)})
	}
	// Upgrade intervals that contain a rational root to exact roots.
	exact := RationalRoots(p)
	for i, r := range roots {
		if r.IsExact() {
			continue
		}
		for _, e := range exact {
			if e.Cmp(r.Lo) > 0 && e.Cmp(r.Hi) < 0 {
				roots[i] = Root{Exact: e, Lo: new(big.Rat).Set(e), Hi: new(big.Rat).Set(e)}
				break
			}
		}
	}
	return roots
}

// descartesBound returns an upper bound (with matching parity) for the number
// of roots of p in the open interval (lo,hi), via Descartes' rule of signs.
func descartesBound(p *Poly, lo, hi *big.Rat) int {
	b := new(big.Rat).Sub(hi, lo)
	q := p.ComposeLinear(lo, b) // q(u) = p(lo + (hi-lo) u), u in (0,1)
	r := q.DescartesTransform() // r(s) = (1+s)^d q(1/(1+s)), s in (0,+inf)
	return r.SignVariations()
}

// refineRoot narrows the isolating interval [lo,hi] (containing a single
// sign-changing root, with p(lo) and p(hi) nonzero) down to width 2^-refineBits
// by exact bisection. If the root is a dyadic rational it is found exactly.
func refineRoot(p *Poly, lo, hi *big.Rat) Root {
	lo = new(big.Rat).Set(lo)
	hi = new(big.Rat).Set(hi)
	slo := p.Sign(lo)
	if slo == 0 {
		return Root{Exact: lo, Lo: lo, Hi: lo}
	}
	half := big.NewRat(1, 2)
	for i := 0; i < refineBits; i++ {
		mid := new(big.Rat).Add(lo, hi)
		mid.Mul(mid, half)
		switch sm := p.Sign(mid); {
		case sm == 0:
			return Root{Exact: mid, Lo: new(big.Rat).Set(mid), Hi: new(big.Rat).Set(mid)}
		case sm == slo:
			lo = mid
		default:
			hi = mid
		}
	}
	return Root{Lo: lo, Hi: hi}
}

// RationalRoots returns all rational roots of p in [0,1], sorted ascending,
// using the rational root theorem on the integer form of p.
func RationalRoots(p *Poly) []*big.Rat {
	ic := p.ToIntPoly()
	d := len(ic) - 1
	if d < 1 {
		return nil
	}
	a0 := new(big.Int).Set(ic[0])
	ad := new(big.Int).Set(ic[d])
	var roots []*big.Rat
	if a0.Sign() == 0 {
		roots = append(roots, rat(0))
	}
	numDivs := divisors(absInt(a0))
	denDivs := divisors(absInt(ad))
	seen := make(map[string]bool)
	for _, num := range numDivs {
		for _, den := range denDivs {
			if num.Sign() == 0 || den.Sign() == 0 {
				continue
			}
			r := new(big.Rat).SetFrac(num, den)
			if r.Sign() < 0 || r.Cmp(rat(1)) > 0 {
				continue
			}
			key := r.RatString()
			if seen[key] {
				continue
			}
			seen[key] = true
			if p.Sign(r) == 0 {
				roots = append(roots, r)
			}
		}
	}
	// sort ascending
	for i := 1; i < len(roots); i++ {
		for j := i; j > 0 && roots[j].Cmp(roots[j-1]) < 0; j-- {
			roots[j], roots[j-1] = roots[j-1], roots[j]
		}
	}
	return roots
}

func absInt(x *big.Int) *big.Int { return new(big.Int).Abs(x) }

// divisors returns all positive divisors of n (n may be 0, giving no divisors).
func divisors(n *big.Int) []*big.Int {
	if n.Sign() == 0 {
		return nil
	}
	var out []*big.Int
	one := big.NewInt(1)
	i := big.NewInt(1)
	q := new(big.Int)
	r := new(big.Int)
	for ; q.Quo(n, i).Cmp(i) >= 0; i.Add(i, one) {
		q.QuoRem(n, i, r)
		if r.Sign() == 0 {
			out = append(out, new(big.Int).Set(i))
			if q.Cmp(i) != 0 {
				out = append(out, new(big.Int).Set(q))
			}
		}
	}
	return out
}
