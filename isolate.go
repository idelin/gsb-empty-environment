package main

import "math/big"

// RootIsolation describes one distinct real root: either an exact rational
// root (Exact != nil), or an open interval (Lower, Upper) containing exactly
// one root, with Lower/Upper themselves not roots.
type RootIsolation struct {
	Lower, Upper *big.Rat
	Exact        *big.Rat
}

// isolateRoots enumerates every distinct real root of the square-free
// polynomial p in the open interval (a, b), returning pairwise-disjoint
// isolating intervals (and exact rational roots) in increasing order.
func isolateRoots(p Poly, a, b *big.Rat) []RootIsolation {
	seq := sturmSequence(p)
	var out []RootIsolation
	two := big.NewRat(2, 1)

	var rec func(l, r *big.Rat, n int)
	rec = func(l, r *big.Rat, n int) {
		if n == 0 {
			return
		}
		// Emit only when endpoints are not roots, so the sign-change
		// evidence at l and r is well defined.
		if n == 1 && p.Eval(l).Sign() != 0 && p.Eval(r).Sign() != 0 {
			out = append(out, RootIsolation{Lower: l, Upper: r})
			return
		}
		mid := new(big.Rat).Add(l, r)
		mid.Quo(mid, two)
		// Roots in (l, mid]: V(l) - V(mid).
		leftCount := signVariations(seq, l) - signVariations(seq, mid)
		if p.Eval(mid).Sign() == 0 {
			leftCount-- // mid itself is a root; exclude it from the open left half
			rec(l, mid, leftCount)
			out = append(out, RootIsolation{Exact: mid})
			rec(mid, r, n-leftCount-1)
			return
		}
		rec(l, mid, leftCount)
		rec(mid, r, n-leftCount)
	}

	rec(a, b, countOpen(seq, p, a, b))
	return out
}
