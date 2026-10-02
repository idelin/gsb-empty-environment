package algebraic

import (
	"math/big"
	"sort"
)

// sturmSeq builds the Sturm sequence of a square-free polynomial.
func sturmSeq(f Poly) []Poly {
	seq := []Poly{f}
	d := f.Deriv()
	if d.IsZero() {
		return seq
	}
	seq = append(seq, d)
	for {
		n := len(seq)
		_, r := seq[n-2].QuoRem(seq[n-1])
		r = r.Neg()
		if r.IsZero() {
			break
		}
		seq = append(seq, r)
	}
	return seq
}

// signVariations counts sign changes of the Sturm sequence at x,
// skipping zero values.
func signVariations(seq []Poly, x *big.Rat) int {
	changes, prev := 0, 0
	for _, p := range seq {
		s := p.Eval(x).Sign()
		if s == 0 {
			continue
		}
		if prev != 0 && s != prev {
			changes++
		}
		prev = s
	}
	return changes
}

// cauchyBound returns a strict bound on the absolute value of all roots.
func cauchyBound(f Poly) *big.Rat {
	lead := f.Lead()
	max := new(big.Rat)
	for i := 0; i < len(f.c)-1; i++ {
		r := new(big.Rat).Abs(new(big.Rat).Quo(f.c[i], lead))
		if r.Cmp(max) > 0 {
			max = r
		}
	}
	return max.Add(max, big.NewRat(1, 1))
}

// sqfrPart returns the square-free part f / gcd(f, f').
func sqfrPart(f Poly) Poly {
	if f.Deg() <= 0 {
		return f
	}
	g := PolyGCD(f, f.Deriv())
	q, _ := f.QuoRem(g)
	return q
}

// Isolate returns disjoint isolating intervals for all real roots of f.
// Each interval (lo, hi) contains exactly one root and neither endpoint
// is a root, except that rational roots of linear factors are returned
// as exact degenerate intervals [r, r].
func Isolate(f Poly) [][2]*big.Rat {
	if f.IsZero() {
		return nil
	}
	g := sqfrPart(f).Monic()
	switch g.Deg() {
	case 0:
		return nil
	case 1:
		r := new(big.Rat).Neg(g.coeff(0))
		return [][2]*big.Rat{{r, r}}
	}
	seq := sturmSeq(g)
	B := cauchyBound(g)
	negB := new(big.Rat).Neg(B)
	rootCount := func(lo, hi *big.Rat) int {
		return signVariations(seq, lo) - signVariations(seq, hi)
	}
	type iv struct {
		lo, hi *big.Rat
		n      int
	}
	stack := []iv{{negB, B, rootCount(negB, B)}}
	var res [][2]*big.Rat
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.n == 0 {
			continue
		}
		if cur.n == 1 {
			res = append(res, [2]*big.Rat{cur.lo, cur.hi})
			continue
		}
		mid := splitPoint(g, cur.lo, cur.hi)
		nl := rootCount(cur.lo, mid)
		stack = append(stack, iv{cur.lo, mid, nl}, iv{mid, cur.hi, cur.n - nl})
	}
	sort.Slice(res, func(i, j int) bool { return res[i][0].Cmp(res[j][0]) < 0 })
	return res
}

// splitPoint returns a point strictly between lo and hi that is not a
// root of f.
func splitPoint(f Poly, lo, hi *big.Rat) *big.Rat {
	mid := new(big.Rat).Add(lo, hi)
	mid.Quo(mid, big.NewRat(2, 1))
	for f.Eval(mid).Sign() == 0 {
		mid.Add(mid, lo)
		mid.Quo(mid, big.NewRat(2, 1))
	}
	return mid
}

// refineRoot halves the isolating interval (lo, hi) of a simple root of f.
// If the root is rational and hit exactly, a degenerate interval [r, r]
// is returned.
func refineRoot(f Poly, lo, hi *big.Rat) (*big.Rat, *big.Rat) {
	if lo.Cmp(hi) == 0 {
		return lo, hi
	}
	flo := f.Eval(lo).Sign()
	if flo == 0 {
		return lo, lo
	}
	mid := new(big.Rat).Add(lo, hi)
	mid.Quo(mid, big.NewRat(2, 1))
	s := f.Eval(mid).Sign()
	if s == 0 {
		return mid, mid
	}
	if (flo < 0) != (s < 0) {
		return lo, mid
	}
	return mid, hi
}
