package rootfind

import (
	"errors"
	"math/big"
	"sort"
)

// Root is a certified root: the polynomial has exactly one distinct root
// of the associated square-free factor inside [Lo, Hi], with multiplicity
// Mult in the original polynomial. Lo == Hi means the root is known
// exactly (a rational root hit during bisection or at an endpoint).
type Root struct {
	Lo   *big.Rat
	Hi   *big.Rat
	Mult int
}

// Contains reports whether r lies in the isolating interval.
func (r Root) Contains(x *big.Rat) bool {
	return r.Lo.Cmp(x) <= 0 && x.Cmp(r.Hi) <= 0
}

// Width returns Hi - Lo.
func (r Root) Width() *big.Rat { return new(big.Rat).Sub(r.Hi, r.Lo) }

// Gap is an interval the solver could not fully resolve before the
// budget ran out. Roots is a certified lower bound on the number of
// distinct roots (of one square-free factor) still inside (Lo, Hi].
type Gap struct {
	Lo    *big.Rat
	Hi    *big.Rat
	Mult  int // multiplicity the contained roots would have
	Roots int // certified number of distinct roots pending inside
}

// Result is the outcome of FindRoots.
type Result struct {
	// Roots holds all certified isolating intervals, sorted by position.
	Roots []Root
	// Incomplete holds intervals left unresolved because the budget was
	// exhausted. Empty means the root set is certified complete.
	Incomplete []Gap
}

// Complete reports whether every root in the search interval was isolated.
func (r Result) Complete() bool { return len(r.Incomplete) == 0 }

// FindRoots finds all real roots of p in the closed interval [lo, hi].
//
// eps is the maximum width of an isolating interval, given as a positive
// rational. budget caps the total number of bisection steps across all
// square-free factors; a negative budget means unlimited. When the budget
// is exhausted, unresolved intervals are reported in Result.Incomplete
// together with a certified count of the roots they still contain.
//
// The polynomial is first decomposed into square-free factors (Yun's
// algorithm) so each factor carries one multiplicity class; roots of each
// factor are then isolated by Sturm-guided bisection. This finds roots of
// even multiplicity that no sign-change sampling could detect.
func FindRoots(p Poly, lo, hi *big.Rat, eps *big.Rat, budget int) (Result, error) {
	var res Result
	if p.IsZero() {
		return res, errors.New("rootfind: zero polynomial has infinitely many roots")
	}
	if lo.Cmp(hi) >= 0 {
		return res, errors.New("rootfind: need lo < hi")
	}
	if eps.Sign() <= 0 {
		return res, errors.New("rootfind: eps must be positive")
	}
	if p.Deg() == 0 {
		return res, nil
	}
	remaining := budget
	for _, f := range squareFreeFactors(p) {
		isolateFactor(f.Q, f.Mult, lo, hi, eps, &remaining, &res)
	}
	sort.Slice(res.Roots, func(i, j int) bool {
		a, b := res.Roots[i], res.Roots[j]
		if c := a.Lo.Cmp(b.Lo); c != 0 {
			return c < 0
		}
		return a.Hi.Cmp(b.Hi) < 0
	})
	sort.Slice(res.Incomplete, func(i, j int) bool {
		return res.Incomplete[i].Lo.Cmp(res.Incomplete[j].Lo) < 0
	})
	return res, nil
}

type factor struct {
	Mult int
	Q    Poly // square-free; its roots are exactly the roots of multiplicity Mult
}

// squareFreeFactors decomposes p as prod Q_m^m with each Q_m square-free
// and pairwise coprime (Yun's algorithm). Only non-constant factors are
// returned, with ascending multiplicity.
func squareFreeFactors(p Poly) []factor {
	var out []factor
	g := GCD(p, p.Deriv())
	w, _ := DivMod(p, g)
	for m := 1; w.Deg() > 0; m++ {
		y := GCD(w, g)
		z, _ := DivMod(w, y)
		if z.Deg() > 0 {
			out = append(out, factor{Mult: m, Q: z})
		}
		w = y
		g, _ = DivMod(g, y)
	}
	return out
}

type workItem struct {
	lo, hi *big.Rat
	count  int // certified number of distinct roots in (lo, hi]
}

// isolateFactor isolates every root of the square-free polynomial f
// inside [lo, hi], tagging results with multiplicity mult.
func isolateFactor(f Poly, mult int, lo, hi *big.Rat, eps *big.Rat, budget *int, res *Result) {
	st := buildSturm(f)

	// The left endpoint is excluded from the Sturm count on (lo, hi],
	// so test it explicitly.
	if f.Eval(lo).Sign() == 0 {
		res.Roots = append(res.Roots, Root{Lo: lo, Hi: lo, Mult: mult})
	}

	total := st.countRoots(lo, hi)
	if total == 0 {
		return
	}
	queue := []workItem{{lo: lo, hi: hi, count: total}}
	for len(queue) > 0 {
		it := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		width := new(big.Rat).Sub(it.hi, it.lo)
		if it.count == 1 && width.Cmp(eps) <= 0 {
			res.Roots = append(res.Roots, Root{Lo: it.lo, Hi: it.hi, Mult: mult})
			continue
		}
		if *budget == 0 {
			res.Incomplete = append(res.Incomplete, Gap{
				Lo: it.lo, Hi: it.hi, Mult: mult, Roots: it.count,
			})
			continue
		}
		if *budget > 0 {
			*budget--
		}

		mid := new(big.Rat).Add(it.lo, it.hi)
		mid.Quo(mid, big.NewRat(2, 1))

		// Roots in (lo, mid]; the rest stay in (mid, hi].
		left := st.countRoots(it.lo, mid)
		right := it.count - left
		if f.Eval(mid).Sign() == 0 {
			// Exact rational root: certify it as a point interval.
			res.Roots = append(res.Roots, Root{Lo: mid, Hi: mid, Mult: mult})
			left--
		}
		if left > 0 {
			queue = append(queue, workItem{lo: it.lo, hi: mid, count: left})
		}
		if right > 0 {
			queue = append(queue, workItem{lo: mid, hi: it.hi, count: right})
		}
	}
}
