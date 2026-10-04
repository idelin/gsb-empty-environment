package main

import (
	"fmt"
	"math/big"
	"sort"
)

// sturm wraps the canonical Sturm sequence of the square-free part of a
// polynomial, so it counts *distinct* real roots even when the original
// polynomial has multiple roots (tangencies).
type sturm struct {
	sf  poly // square-free part of the source polynomial
	seq []poly
}

func newSturm(p poly) *sturm {
	if p.isZero() {
		panic("sturm: zero polynomial has infinitely many roots")
	}
	sf := p
	if p.deg() >= 1 {
		sf = quoExact(p, gcdPoly(p, derivPoly(p)))
	}
	seq := []poly{sf}
	if sf.deg() >= 1 {
		seq = append(seq, derivPoly(sf))
		for {
			n := len(seq)
			_, r := divmodPoly(seq[n-2], seq[n-1])
			if r.isZero() {
				break
			}
			seq = append(seq, negPoly(r))
		}
	}
	return &sturm{sf: sf, seq: seq}
}

// signVar counts sign variations of the Sturm sequence at x, skipping zeros.
func (s *sturm) signVar(x *big.Rat) int {
	vars, prev := 0, 0
	for _, q := range s.seq {
		v := evalPoly(q, x).Sign()
		if v == 0 {
			continue
		}
		if prev != 0 && v != prev {
			vars++
		}
		prev = v
	}
	return vars
}

// countOpen returns the number of distinct real roots in the open interval
// (a, b). Sturm's theorem gives roots in (a, b] as signVar(a)-signVar(b);
// subtract an exact root at b to get the open interval.
func (s *sturm) countOpen(a, b *big.Rat) int {
	n := s.signVar(a) - s.signVar(b)
	if evalPoly(s.sf, b).Sign() == 0 {
		n--
	}
	return n
}

// root is one distinct real root of a polynomial: either an exact rational
// value, or an open isolating interval (lo, hi) containing exactly one root.
type root struct {
	exact *big.Rat
	lo    *big.Rat
	hi    *big.Rat
	mult  int // multiplicity in the source polynomial (1 = simple)
}

func (rt root) String() string {
	if rt.exact != nil {
		return ratStr(rt.exact)
	}
	mid := new(big.Rat).Add(rt.lo, rt.hi)
	mid.Quo(mid, big.NewRat(2, 1))
	return fmt.Sprintf("~%s in (%s, %s)",
		mid.FloatString(9), rt.lo.FloatString(9), rt.hi.FloatString(9))
}

// findRoots finds every distinct real root of p in [l, r), or in [l, r] when
// closedRight is set (used for the final sub-interval so the domain's right
// endpoint is included). Rational roots are reported exactly (rational root
// theorem); irrational roots are reported as isolating intervals with
// rational bounds. p must not be the zero polynomial: an identically zero
// difference means interval coincidence and is handled by the caller, never
// by root isolation (which would loop forever).
func findRoots(p poly, l, r *big.Rat, closedRight bool, eps *big.Rat) []root {
	if p.isZero() {
		panic("findRoots: zero polynomial (coincident interval, not roots)")
	}
	var inner []root
	rats, rest := rationalRoots(p)
	for _, rt := range rats {
		if rt.exact.Cmp(l) > 0 && rt.exact.Cmp(r) < 0 {
			inner = append(inner, rt)
		}
	}
	if rest.deg() >= 1 {
		inner = append(inner, isolateOpen(newSturm(rest), l, r, eps)...)
	}
	sort.Slice(inner, func(i, j int) bool { return rootLess(inner[i], inner[j]) })
	var out []root
	if evalPoly(p, l).Sign() == 0 {
		out = append(out, root{exact: new(big.Rat).Set(l), mult: 1})
	}
	out = append(out, inner...)
	if closedRight && evalPoly(p, r).Sign() == 0 {
		out = append(out, root{exact: new(big.Rat).Set(r), mult: 1})
	}
	return out
}

func rootKey(rt root) *big.Rat {
	if rt.exact != nil {
		return rt.exact
	}
	m := new(big.Rat).Add(rt.lo, rt.hi)
	return m.Quo(m, big.NewRat(2, 1))
}

func rootLess(a, b root) bool { return rootKey(a).Cmp(rootKey(b)) < 0 }

// rationalRoots extracts all rational roots of p exactly (with multiplicity)
// via the rational root theorem, and returns the remaining factor, which has
// no rational roots left.
func rationalRoots(p poly) ([]root, poly) {
	rest := p
	var roots []root
	add := func(x *big.Rat) {
		if n := len(roots); n > 0 && roots[n-1].exact.Cmp(x) == 0 {
			roots[n-1].mult++
			return
		}
		roots = append(roots, root{exact: new(big.Rat).Set(x), mult: 1})
	}
	zero := new(big.Rat)
	for rest.deg() >= 1 && evalPoly(rest, zero).Sign() == 0 {
		rest = quoExact(rest, poly{new(big.Rat), big.NewRat(1, 1)})
		add(zero)
	}
	for rest.deg() >= 1 {
		found := false
		for _, cand := range rationalRootCandidates(rest) {
			if evalPoly(rest, cand).Sign() != 0 {
				continue
			}
			factor := poly{new(big.Rat).Neg(cand), big.NewRat(1, 1)}
			for rest.deg() >= 1 && evalPoly(rest, cand).Sign() == 0 {
				rest = quoExact(rest, factor)
				add(cand)
			}
			found = true
			break
		}
		if !found {
			break
		}
	}
	return roots, rest
}

// rationalRootCandidates enumerates candidate rational roots of p using the
// rational root theorem: after clearing denominators to integer coefficients
// a0 + ... + an x^n, every rational root in lowest terms is c/d with
// c | a0 and d | an.
func rationalRootCandidates(p poly) []*big.Rat {
	lcm := big.NewInt(1)
	g := new(big.Int)
	for _, c := range p {
		d := c.Denom()
		g.GCD(nil, nil, lcm, d)
		lcm.Quo(lcm, g)
		lcm.Mul(lcm, d)
	}
	intCoeff := func(c *big.Rat) *big.Int {
		q := new(big.Int).Quo(lcm, c.Denom())
		return q.Mul(q, c.Num())
	}
	a0 := intCoeff(p[0])
	an := intCoeff(p.lead())
	seen := map[string]bool{}
	var out []*big.Rat
	for _, c := range divisors(a0) {
		for _, d := range divisors(an) {
			for _, s := range []int{1, -1} {
				r := new(big.Rat).SetFrac(c, d)
				if s < 0 {
					r.Neg(r)
				}
				key := r.RatString()
				if !seen[key] {
					seen[key] = true
					out = append(out, r)
				}
			}
		}
	}
	return out
}

// divisors returns the positive divisors of n (n != 0).
func divisors(n *big.Int) []*big.Int {
	a := new(big.Int).Abs(n)
	var out []*big.Int
	i := big.NewInt(1)
	one := big.NewInt(1)
	sq, q, rem := new(big.Int), new(big.Int), new(big.Int)
	for sq.Mul(i, i).Cmp(a) <= 0 {
		q.QuoRem(a, i, rem)
		if rem.Sign() == 0 {
			out = append(out, new(big.Int).Set(i))
			if q.Cmp(i) != 0 {
				out = append(out, new(big.Int).Set(q))
			}
		}
		i.Add(i, one)
	}
	return out
}

// isolateOpen isolates all distinct roots in the open interval (l, r) by
// exact rational bisection driven by Sturm root counts.
func isolateOpen(st *sturm, l, r *big.Rat, eps *big.Rat) []root {
	var out []root
	var rec func(a, b *big.Rat, n int)
	rec = func(a, b *big.Rat, n int) {
		if n == 0 {
			return
		}
		if n == 1 {
			out = append(out, refineRoot(st, a, b, eps))
			return
		}
		m := new(big.Rat).Add(a, b)
		m.Quo(m, big.NewRat(2, 1))
		nl := st.countOpen(a, m)
		nr := n - nl
		exactMid := evalPoly(st.sf, m).Sign() == 0
		if exactMid {
			nr--
		}
		rec(a, m, nl)
		if exactMid {
			out = append(out, root{exact: m, mult: 1})
		}
		rec(m, b, nr)
	}
	rec(l, r, st.countOpen(l, r))
	return out
}

// refineRoot narrows an interval known to contain exactly one root until its
// width is at most eps, or until the root is hit exactly.
func refineRoot(st *sturm, a, b *big.Rat, eps *big.Rat) root {
	two := big.NewRat(2, 1)
	w := new(big.Rat).Sub(b, a)
	for w.Cmp(eps) > 0 {
		m := new(big.Rat).Add(a, b)
		m.Quo(m, two)
		if evalPoly(st.sf, m).Sign() == 0 {
			return root{exact: m, mult: 1}
		}
		if st.countOpen(a, m) == 1 {
			b = m
		} else {
			a = m
		}
		w.Sub(b, a)
	}
	return root{lo: a, hi: b, mult: 1}
}

// assignMult sets the multiplicity of each root using Yun's square-free
// factorization: a root of the i-th square-free factor has multiplicity i.
// Even multiplicity means a tangency (curves touch); it still counts as an
// equality point.
func assignMult(p poly, roots []root) {
	factors := squarefreeFactors(p)
	sts := make([]*sturm, len(factors))
	for i, a := range factors {
		if a.deg() > 0 {
			sts[i] = newSturm(a)
		}
	}
	for j := range roots {
		rt := &roots[j]
		for i, a := range factors {
			if a.deg() <= 0 {
				continue
			}
			if rt.exact != nil {
				if evalPoly(a, rt.exact).Sign() == 0 {
					rt.mult = i + 1
					break
				}
			} else if sts[i].countOpen(rt.lo, rt.hi) == 1 {
				rt.mult = i + 1
				break
			}
		}
	}
}
