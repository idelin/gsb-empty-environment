package ratsimp

import (
	"fmt"
	"math/big"
	"sort"
)

// Interval is an exact isolating interval for a real root: it contains
// exactly one real root r of the associated polynomial, Lo and Hi are
// exact rationals, and Lo < r <= Hi.
type Interval struct {
	Lo, Hi *big.Rat
}

func (iv Interval) String() string {
	mid := new(big.Rat).Add(iv.Lo, iv.Hi)
	mid.Quo(mid, big.NewRat(2, 1))
	f, _ := mid.Float64()
	return fmt.Sprintf("(%s, %s] ~ %.6g", iv.Lo.RatString(), iv.Hi.RatString(), f)
}

// Contains reports whether x lies in (Lo, Hi].
func (iv Interval) Contains(x *big.Rat) bool {
	return iv.Lo.Cmp(x) < 0 && iv.Hi.Cmp(x) >= 0
}

// Width returns Hi - Lo.
func (iv Interval) Width() *big.Rat {
	return new(big.Rat).Sub(iv.Hi, iv.Lo)
}

// sqFactor is one square-free factor of a polynomial together with the
// multiplicity with which it occurs.
type sqFactor struct {
	f    rpoly
	mult int
}

// squareFree computes the square-free factorization of p over the
// rationals (Yun's algorithm): p = prod f_i^i with each f_i
// square-free and the f_i pairwise coprime.
func squareFree(p rpoly) []sqFactor {
	var out []sqFactor
	if p.deg() < 1 {
		return out
	}
	c := rgcd(p, p.deriv())
	w, _ := p.quoRem(c)
	for i := 1; w.deg() > 0; i++ {
		y := rgcd(w, c)
		z, _ := w.quoRem(y)
		if z.deg() > 0 {
			out = append(out, sqFactor{f: z.monic(), mult: i})
		}
		w = y
		c, _ = c.quoRem(y)
	}
	return out
}

// sturmSeq builds the Sturm sequence of a square-free polynomial.
func sturmSeq(p rpoly) []rpoly {
	seq := []rpoly{p, p.deriv()}
	for !seq[len(seq)-1].isZero() {
		_, rem := seq[len(seq)-2].quoRem(seq[len(seq)-1])
		seq = append(seq, rem.neg())
	}
	return seq[:len(seq)-1]
}

func eval(r rpoly, x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(r.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x).Add(acc, r.c[i])
	}
	return acc
}

// signVariations counts sign changes in the Sturm sequence evaluated
// at x, ignoring zeros.
func signVariations(seq []rpoly, x *big.Rat) int {
	count := 0
	prev := 0
	for _, s := range seq {
		sign := eval(s, x).Sign()
		if sign == 0 {
			continue
		}
		if prev != 0 && sign != prev {
			count++
		}
		prev = sign
	}
	return count
}

// rootBound returns a rational M such that every real root r of p
// satisfies -M < r < M (Cauchy bound plus one).
func rootBound(p rpoly) *big.Rat {
	n := len(p.c) - 1
	max := new(big.Rat)
	lc := new(big.Rat).Abs(p.c[n])
	for i := 0; i < n; i++ {
		t := new(big.Rat).Quo(new(big.Rat).Abs(p.c[i]), lc)
		if t.Cmp(max) > 0 {
			max = t
		}
	}
	return max.Add(max, big.NewRat(2, 1))
}

// countRoots counts the distinct real roots of the square-free
// polynomial with Sturm sequence seq in the half-open interval (l, r].
func countRoots(seq []rpoly, l, r *big.Rat) int {
	return signVariations(seq, l) - signVariations(seq, r)
}

// isolateRoots isolates every real root of the square-free polynomial
// p into its own interval with rational endpoints, in increasing order.
// Adjacent intervals are refined until they are strictly separated.
func isolateRoots(p rpoly) []Interval {
	if p.deg() < 1 {
		return nil
	}
	seq := sturmSeq(p)
	m := rootBound(p)
	type item struct{ lo, hi *big.Rat }
	work := []item{{new(big.Rat).Neg(m), m}}
	var out []Interval
	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		n := countRoots(seq, it.lo, it.hi)
		if n == 0 {
			continue
		}
		if n == 1 {
			out = append(out, Interval{it.lo, it.hi})
			continue
		}
		mid := new(big.Rat).Add(it.lo, it.hi)
		mid.Quo(mid, big.NewRat(2, 1))
		work = append(work, item{it.lo, mid}, item{mid, it.hi})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lo.Cmp(out[j].Lo) < 0 })
	// Separate adjacent intervals strictly so that the isolating
	// intervals are pairwise disjoint with a gap between them.
	for i := 0; i+1 < len(out); i++ {
		for out[i].Hi.Cmp(out[i+1].Lo) >= 0 {
			out[i] = bisectOnce(seq, out[i])
			out[i+1] = bisectOnce(seq, out[i+1])
		}
	}
	return out
}

// bisectOnce halves an isolating interval, keeping the root inside.
func bisectOnce(seq []rpoly, iv Interval) Interval {
	mid := new(big.Rat).Add(iv.Lo, iv.Hi)
	mid.Quo(mid, big.NewRat(2, 1))
	if countRoots(seq, iv.Lo, mid) >= 1 {
		return Interval{iv.Lo, mid}
	}
	return Interval{mid, iv.Hi}
}

// refineInterval narrows an isolating interval of the square-free
// polynomial p to width at most maxWidth.
func refineInterval(p rpoly, iv Interval, maxWidth *big.Rat) Interval {
	seq := sturmSeq(p)
	lo, hi := iv.Lo, iv.Hi
	for new(big.Rat).Sub(hi, lo).Cmp(maxWidth) > 0 {
		mid := new(big.Rat).Add(lo, hi)
		mid.Quo(mid, big.NewRat(2, 1))
		if countRoots(seq, lo, mid) >= 1 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return Interval{lo, hi}
}
