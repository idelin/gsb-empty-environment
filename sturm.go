package rootfind

import "math/big"

// SturmSeq returns the Sturm sequence of f, which must be square-free
// (the caller guarantees this; SquareFreePart produces such an f).
// The sequence ends with a nonzero constant.
func SturmSeq(f Poly) []Poly {
	if f.IsConst() {
		return []Poly{f}
	}
	seq := []Poly{f, f.Deriv()}
	for {
		n := len(seq)
		r := seq[n-2].Rem(seq[n-1]).Neg()
		if r.IsZero() {
			break
		}
		seq = append(seq, r)
		if r.IsConst() {
			break
		}
	}
	return seq
}

// signVariations counts sign changes in the Sturm sequence evaluated at x.
// Zero values are skipped, which is exactly the convention under which
// Sturm's theorem counts roots of a square-free polynomial in (a, b]
// as V(a) - V(b), even when a or b is itself a root.
func signVariations(seq []Poly, x *big.Rat) int {
	vars := 0
	prev := 0
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

// countRoots returns the exact number of distinct real roots of the
// square-free polynomial underlying seq that lie in (a, b].
func countRoots(seq []Poly, a, b *big.Rat) int {
	return signVariations(seq, a) - signVariations(seq, b)
}
