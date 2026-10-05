package main

import "math/big"

// sturmSequence builds the Sturm chain of p: p0 = p, p1 = p',
// p_{i+1} = -rem(p_{i-1}, p_i), stopping at the zero remainder.
// All arithmetic is exact over Q.
func sturmSequence(p Poly) []Poly {
	seq := []Poly{p}
	d := p.Derivative()
	if d.IsZero() {
		return seq
	}
	seq = append(seq, d)
	for {
		n := len(seq)
		_, r := seq[n-2].DivMod(seq[n-1])
		if r.IsZero() {
			break
		}
		seq = append(seq, r.Neg())
	}
	return seq
}

// signVariations counts sign changes in the Sturm chain evaluated at x,
// skipping zero entries.
func signVariations(seq []Poly, x *big.Rat) int {
	vars, prev := 0, 0
	for _, s := range seq {
		sign := s.Eval(x).Sign()
		if sign == 0 {
			continue
		}
		if prev != 0 && sign != prev {
			vars++
		}
		prev = sign
	}
	return vars
}

// countOpen returns the number of distinct real roots of p in the open
// interval (a, b). By Sturm's theorem, V(a)-V(b) counts roots in (a, b]
// (this holds even when a or b are roots, since zeros are skipped in V),
// so we subtract one when b itself is a root.
func countOpen(seq []Poly, p Poly, a, b *big.Rat) int {
	n := signVariations(seq, a) - signVariations(seq, b)
	if p.Eval(b).Sign() == 0 {
		n--
	}
	return n
}
