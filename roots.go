package rational

import (
	"fmt"
	"math/big"
	"sort"
)

// IsolatedRoot is an exact representation of one real root of a
// square-free polynomial: the unique root of Poly in the open rational
// interval (Lo, Hi). Neither endpoint is a root. The interval can be
// refined to arbitrary precision without any floating-point error.
type IsolatedRoot struct {
	Lo   *big.Rat
	Hi   *big.Rat
	poly *Poly
}

// Poly returns the square-free polynomial this root belongs to.
func (r *IsolatedRoot) Poly() *Poly { return r.poly }

// Width returns Hi - Lo.
func (r *IsolatedRoot) Width() *big.Rat {
	return new(big.Rat).Sub(r.Hi, r.Lo)
}

// Midpoint returns the rational midpoint of the isolating interval.
func (r *IsolatedRoot) Midpoint() *big.Rat {
	m := new(big.Rat).Add(r.Lo, r.Hi)
	return m.Quo(m, big.NewRat(2, 1))
}

// Approx returns a float64 approximation of the root.
func (r *IsolatedRoot) Approx() float64 {
	f, _ := r.Midpoint().Float64()
	return f
}

// Contains reports whether the rational x lies strictly inside the
// isolating interval.
func (r *IsolatedRoot) Contains(x *big.Rat) bool {
	return r.Lo.Cmp(x) < 0 && r.Hi.Cmp(x) > 0
}

// Refine halves the isolating interval (or shrinks around an exactly
// hit rational root). The interval always keeps exactly one root.
func (r *IsolatedRoot) Refine() {
	mid := r.Midpoint()
	s := r.poly.SignAt(mid)
	switch {
	case s == 0:
		// mid is exactly the root; shrink symmetrically around it.
		quarter := new(big.Rat).Quo(r.Width(), big.NewRat(4, 1))
		r.Lo = new(big.Rat).Sub(mid, quarter)
		r.Hi = new(big.Rat).Add(mid, quarter)
	case s == r.poly.SignAt(r.Lo):
		r.Lo = mid
	default:
		r.Hi = mid
	}
}

// RefineTo refines the interval until its width is at most maxWidth.
func (r *IsolatedRoot) RefineTo(maxWidth *big.Rat) {
	for r.Width().Cmp(maxWidth) > 0 {
		r.Refine()
	}
}

func (r *IsolatedRoot) String() string {
	return fmt.Sprintf("(%s, %s) ~ %.8g", r.Lo.RatString(), r.Hi.RatString(), r.Approx())
}

// sturmSequence returns the Sturm sequence of p: p, p', then successive
// negated remainders, excluding the final zero.
func sturmSequence(p *Poly) []*Poly {
	seq := []*Poly{p, p.Derivative()}
	for !seq[len(seq)-1].IsZero() {
		_, r := DivMod(seq[len(seq)-2], seq[len(seq)-1])
		seq = append(seq, r.Neg())
	}
	return seq[:len(seq)-1]
}

// signVariations counts sign changes in the Sturm sequence evaluated at
// x, ignoring zeros.
func signVariations(seq []*Poly, x *big.Rat) int {
	variations, prev := 0, 0
	for _, s := range seq {
		sign := s.SignAt(x)
		if sign == 0 {
			continue
		}
		if prev != 0 && sign != prev {
			variations++
		}
		prev = sign
	}
	return variations
}

// sturmCount returns the number of distinct real roots of p in (a, b].
// a and b must not be roots of p.
func sturmCount(seq []*Poly, a, b *big.Rat) int {
	return signVariations(seq, a) - signVariations(seq, b)
}

// cauchyBound returns a strict rational bound B such that every real
// root of p lies in (-B, B).
func cauchyBound(p *Poly) *big.Rat {
	lc := p.LeadingCoeff()
	max := new(big.Rat)
	for i := 0; i < p.Degree(); i++ {
		r := new(big.Rat).Quo(p.Coeff(i), lc)
		r.Abs(r)
		if r.Cmp(max) > 0 {
			max.Set(r)
		}
	}
	// 2 + max ratio is strictly larger than any root modulus.
	return max.Add(max, big.NewRat(2, 1))
}

// numRealRoots counts the distinct real roots of p exactly.
func numRealRoots(p *Poly) int {
	if p.Degree() <= 0 {
		return 0
	}
	sf := p.SquareFreePart()
	seq := sturmSequence(sf)
	b := cauchyBound(sf)
	return sturmCount(seq, new(big.Rat).Neg(b), b)
}

// isolateRealRoots returns the distinct real roots of p as disjoint
// isolating intervals, sorted ascending. p need not be square-free.
func isolateRealRoots(p *Poly) []IsolatedRoot {
	if p.Degree() <= 0 {
		return nil
	}
	sf := p.SquareFreePart()
	seq := sturmSequence(sf)
	b := cauchyBound(sf)
	lo := new(big.Rat).Neg(b)
	var roots []IsolatedRoot
	var rec func(lo, hi *big.Rat, n int)
	rec = func(lo, hi *big.Rat, n int) {
		if n == 0 {
			return
		}
		if n == 1 {
			roots = append(roots, IsolatedRoot{
				Lo:   new(big.Rat).Set(lo),
				Hi:   new(big.Rat).Set(hi),
				poly: sf,
			})
			return
		}
		width := new(big.Rat).Sub(hi, lo)
		half := big.NewRat(1, 2)
		mid := new(big.Rat).Add(lo, new(big.Rat).Mul(half, width))
		// If the midpoint lands exactly on a root, nudge it
		// deterministically toward (but never onto) another
		// rational point inside the interval.
		for k := int64(2); sf.SignAt(mid) == 0; k++ {
			frac := new(big.Rat).Add(half, big.NewRat(1, 1<<uint(k)))
			mid.Add(lo, new(big.Rat).Mul(frac, width))
		}
		left := sturmCount(seq, lo, mid)
		rec(lo, mid, left)
		rec(mid, hi, n-left)
	}
	rec(lo, b, sturmCount(seq, lo, b))
	return roots
}

// sortRoots orders roots ascending, refining intervals until they are
// pairwise disjoint so the order is exact.
func sortRoots(roots []IsolatedRoot) {
	for {
		sort.Slice(roots, func(i, j int) bool {
			return roots[i].Lo.Cmp(roots[j].Lo) < 0
		})
		overlapped := false
		for i := 0; i+1 < len(roots); i++ {
			if roots[i].Hi.Cmp(roots[i+1].Lo) > 0 {
				roots[i].Refine()
				roots[i+1].Refine()
				overlapped = true
			}
		}
		if !overlapped {
			return
		}
	}
}

// sameRealRootSet reports whether a and b vanish at exactly the same
// real points, i.e. whether 1/a and 1/b have the same domain.
func sameRealRootSet(a, b *Poly) bool {
	sfa, sfb := a.SquareFreePart(), b.SquareFreePart()
	common := numRealRoots(GCD(sfa, sfb))
	return numRealRoots(sfa) == common && numRealRoots(sfb) == common
}
