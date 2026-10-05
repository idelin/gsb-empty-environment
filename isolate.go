package rootfind

import (
	"fmt"
	"math/big"
	"sort"
)

// Root is an isolated real root of the input polynomial.
type Root struct {
	// The root is guaranteed to lie in (Lo, Hi], with Hi-Lo <= precision,
	// unless Exact is set.
	Lo, Hi *big.Rat
	// Exact is non-nil when the root is known exactly (a rational root
	// lying on the search-interval boundary or on a bisection point);
	// then Lo == Hi == Exact.
	Exact *big.Rat
	// Mult is the exact multiplicity of the root in the input polynomial,
	// obtained from Yun's square-free factorization, not from numerics.
	Mult int
}

// Contains reports whether r lies in the isolating interval.
func (rt Root) Contains(r *big.Rat) bool {
	if rt.Exact != nil {
		return r.Cmp(rt.Exact) == 0
	}
	return r.Cmp(rt.Lo) > 0 && r.Cmp(rt.Hi) <= 0
}

func (rt Root) String() string {
	if rt.Exact != nil {
		return fmt.Sprintf("root = %s exactly, multiplicity %d", rt.Exact.RatString(), rt.Mult)
	}
	return fmt.Sprintf("root in (%s, %s], width %s, multiplicity %d",
		rt.Lo.RatString(), rt.Hi.RatString(),
		new(big.Rat).Sub(rt.Hi, rt.Lo).RatString(), rt.Mult)
}

// Gap is an interval the isolator could not finish within the budget.
// It is known to contain Roots distinct real roots.
type Gap struct {
	Lo, Hi *big.Rat
	Roots  int
}

// Result is the outcome of Isolate.
type Result struct {
	Roots []Root
	// Unfinished lists intervals still known to contain roots when the
	// bisection budget ran out. Empty means the isolation is complete.
	Unfinished []Gap
	// Steps is the number of bisections actually performed.
	Steps int
}

// Complete reports whether every root in the search interval was isolated.
func (r Result) Complete() bool { return len(r.Unfinished) == 0 }

// workItem is a half-open interval (lo, hi] together with the exact
// number of distinct, not-yet-reported roots inside it. The half-open
// convention is what keeps Sturm endpoint counts consistent under
// subdivision: a root on a shared boundary belongs to exactly one child.
type workItem struct {
	lo, hi *big.Rat
	n      int
}

// Isolate finds all real roots of p in the closed interval [a, b].
//
// Completeness rests on Sturm's theorem applied to the square-free part of
// p: every interval in the work queue carries an exact count of the
// distinct roots inside it, and an interval is discarded only when that
// count is 0. Even-multiplicity roots, which produce no sign change and
// are invisible to sampling, are therefore still found. Multiplicities
// come from Yun's square-free factorization. All arithmetic is exact
// rational arithmetic.
//
// prec is the maximum width of a reported isolating interval (a positive
// rational). budget bounds the number of bisections; a negative budget
// means unlimited. If the budget is exhausted, intervals that still
// contain roots are reported in Result.Unfinished and Complete returns
// false.
func Isolate(p Poly, a, b *big.Rat, prec *big.Rat, budget int) Result {
	if p.IsZero() {
		panic("rootfind: zero polynomial has infinitely many roots")
	}
	if a.Cmp(b) >= 0 {
		panic("rootfind: empty search interval")
	}
	if prec.Sign() <= 0 {
		panic("rootfind: precision must be positive")
	}

	var res Result
	if p.IsConst() {
		return res // nonzero constant: no roots anywhere
	}

	q := SquareFreePart(p)
	seq := SturmSeq(q)
	type factorSturm struct {
		f   Poly
		exp int
		seq []Poly
	}
	var factors []factorSturm
	for _, sf := range SqFree(p) {
		factors = append(factors, factorSturm{f: sf.F, exp: sf.Exp, seq: SturmSeq(sf.F)})
	}

	multAt := func(r *big.Rat) int {
		for _, fc := range factors {
			if fc.f.Eval(r).Sign() == 0 {
				return fc.exp
			}
		}
		panic("rootfind: internal error, no square-free factor vanishes at exact root")
	}
	multIn := func(lo, hi *big.Rat) int {
		for _, fc := range factors {
			if countRoots(fc.seq, lo, hi) == 1 {
				return fc.exp
			}
		}
		panic("rootfind: internal error, no square-free factor has a root in isolating interval")
	}
	emitExact := func(r *big.Rat) {
		res.Roots = append(res.Roots, Root{
			Lo: new(big.Rat).Set(r), Hi: new(big.Rat).Set(r),
			Exact: new(big.Rat).Set(r), Mult: multAt(r),
		})
	}

	// A root exactly at the left endpoint a is not covered by the (a, b]
	// counting convention, so handle it once up front.
	if q.Eval(a).Sign() == 0 {
		emitExact(a)
	}

	var queue []workItem
	if n := countRoots(seq, a, b); n > 0 {
		queue = append(queue, workItem{lo: a, hi: b, n: n})
	}

	two := big.NewRat(2, 1)
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		width := new(big.Rat).Sub(it.hi, it.lo)
		if it.n == 1 && width.Cmp(prec) <= 0 {
			// The single root lies in (lo, hi]. If q vanishes at hi,
			// the root is hi exactly (roots at lo are excluded by the
			// half-open convention and were accounted for elsewhere).
			if q.Eval(it.hi).Sign() == 0 {
				emitExact(it.hi)
			} else {
				res.Roots = append(res.Roots, Root{
					Lo: it.lo, Hi: it.hi, Mult: multIn(it.lo, it.hi),
				})
			}
			continue
		}
		if budget >= 0 && res.Steps >= budget {
			res.Unfinished = append(res.Unfinished, Gap{Lo: it.lo, Hi: it.hi, Roots: it.n})
			continue
		}
		res.Steps++
		mid := new(big.Rat).Quo(new(big.Rat).Add(it.lo, it.hi), two)
		// Sturm counts are exact for half-open intervals, so the two
		// children partition the parent's roots with no double counting,
		// even when mid itself is a root (it belongs to the left child).
		if left := countRoots(seq, it.lo, mid); left > 0 {
			queue = append(queue, workItem{lo: it.lo, hi: mid, n: left})
		}
		if right := countRoots(seq, mid, it.hi); right > 0 {
			queue = append(queue, workItem{lo: mid, hi: it.hi, n: right})
		}
	}

	sort.Slice(res.Roots, func(i, j int) bool {
		return res.Roots[i].Hi.Cmp(res.Roots[j].Hi) < 0
	})
	return res
}
