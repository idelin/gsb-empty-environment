package main

import "math/big"

// Interval [L, R] with rational endpoints; L == R means an exact rational point.
type Interval struct {
	L, R *big.Rat
}

func (iv Interval) width() *big.Rat { return new(big.Rat).Sub(iv.R, iv.L) }

func (iv Interval) contains(x *big.Rat) bool {
	return iv.L.Cmp(x) <= 0 && x.Cmp(iv.R) <= 0
}

func avg(a, b *big.Rat) *big.Rat {
	r := new(big.Rat).Add(a, b)
	return r.Quo(r, ratI(2))
}

// sturmSeq builds the Sturm sequence of p (p must be square-free for
// root-counting to be meaningful).
func sturmSeq(p Poly) []Poly {
	seq := []Poly{p}
	d := derivP(p)
	if d.Zero() {
		return seq
	}
	seq = append(seq, d)
	for {
		n := len(seq)
		_, r := divModP(seq[n-2], seq[n-1])
		if r.Zero() {
			break
		}
		seq = append(seq, negP(r))
	}
	return seq
}

// signVar counts sign variations of the Sturm sequence at x, skipping zeros.
func signVar(seq []Poly, x *big.Rat) int {
	v, prev := 0, 0
	for _, s := range seq {
		sg := s.eval(x).Sign()
		if sg == 0 {
			continue
		}
		if prev != 0 && sg != prev {
			v++
		}
		prev = sg
	}
	return v
}

// countRoots counts distinct real roots of the (square-free) polynomial
// in the half-open interval (a, b].
func countRoots(seq []Poly, a, b *big.Rat) int {
	return signVar(seq, a) - signVar(seq, b)
}

// isolateRec emits isolating intervals for all roots in (a, b], in order.
// va = signVar(a), vb = signVar(b).
func isolateRec(p Poly, seq []Poly, a, b *big.Rat, va, vb int, out *[]Interval) {
	if va-vb <= 0 {
		return
	}
	if va-vb == 1 {
		*out = append(*out, Interval{new(big.Rat).Set(a), new(big.Rat).Set(b)})
		return
	}
	mid := avg(a, b)
	if p.eval(mid).Sign() == 0 {
		vm := signVar(seq, mid)
		// roots strictly inside (a, mid): va - vm - 1 (mid itself counted in va-vm)
		isolateRec(p, seq, a, mid, va, vm+1, out)
		*out = append(*out, Interval{new(big.Rat).Set(mid), new(big.Rat).Set(mid)})
		isolateRec(p, seq, mid, b, vm, vb, out)
		return
	}
	vm := signVar(seq, mid)
	isolateRec(p, seq, a, mid, va, vm, out)
	isolateRec(p, seq, mid, b, vm, vb, out)
}

// isolateInterior isolates all roots of square-free q in the open interval
// (0, 1), refined to width <= eps with 0 < L <= R < 1.
func isolateInterior(q Poly, eps *big.Rat) []Interval {
	if q.Deg() < 1 {
		return nil
	}
	seq := sturmSeq(q)
	zero, one := ratI(0), ratI(1)
	var ivs []Interval
	isolateRec(q, seq, zero, one, signVar(seq, zero), signVar(seq, one), &ivs)
	// Drop a root sitting exactly at t = 1 (endpoints are handled separately).
	kept := ivs[:0]
	for _, iv := range ivs {
		if iv.R.Cmp(one) == 0 && q.eval(one).Sign() == 0 {
			continue
		}
		kept = append(kept, iv)
	}
	for i := range kept {
		kept[i] = refineRoot(q, seq, kept[i], eps)
	}
	return kept
}

// refineRoot shrinks an isolating interval to width <= eps, also ensuring
// 0 < L and R < 1 (possible because the root is strictly interior).
func refineRoot(p Poly, seq []Poly, iv Interval, eps *big.Rat) Interval {
	if iv.L.Cmp(iv.R) == 0 {
		return iv
	}
	for iv.width().Cmp(eps) > 0 || iv.L.Sign() == 0 || iv.R.Cmp(ratI(1)) == 0 {
		iv = bisectOnce(p, seq, iv)
		if iv.L.Cmp(iv.R) == 0 {
			return iv
		}
	}
	return iv
}

// bisectOnce halves an isolating interval, keeping the unique root.
func bisectOnce(p Poly, seq []Poly, iv Interval) Interval {
	mid := avg(iv.L, iv.R)
	if p.eval(mid).Sign() == 0 {
		return Interval{mid, mid}
	}
	if countRoots(seq, iv.L, mid) == 1 {
		return Interval{iv.L, mid}
	}
	return Interval{mid, iv.R}
}

// refineRoots refines each isolating interval to width <= delta.
func refineRoots(p Poly, ivs []Interval, delta *big.Rat) []Interval {
	seq := sturmSeq(p)
	out := make([]Interval, len(ivs))
	for i, iv := range ivs {
		for iv.L.Cmp(iv.R) != 0 && iv.width().Cmp(delta) > 0 {
			iv = bisectOnce(p, seq, iv)
		}
		out[i] = iv
	}
	return out
}
