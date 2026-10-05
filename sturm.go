package algcmp

import "math/big"

// sturmSequence builds the Sturm sequence of p:
// s0 = p, s1 = p', s_{i+1} = -rem(s_{i-1}, s_i), stopping at the
// last nonzero remainder.
func sturmSequence(p Poly) []Poly {
	seq := []Poly{p, p.Deriv()}
	for !seq[len(seq)-1].IsZero() {
		_, rem := DivMod(seq[len(seq)-2], seq[len(seq)-1])
		next := rem.Neg()
		if next.IsZero() {
			break
		}
		seq = append(seq, next)
	}
	return seq
}

// signVariations counts sign changes in the Sturm sequence evaluated
// at x, ignoring zero entries (the standard Sturm convention).
func signVariations(seq []Poly, x *big.Rat) int {
	prev, count := 0, 0
	for _, f := range seq {
		s := f.SignAt(x)
		if s == 0 {
			continue
		}
		if prev != 0 && s != prev {
			count++
		}
		prev = s
	}
	return count
}

// CountRoots counts the distinct real roots of p in the open interval
// (l, r) via Sturm's theorem.  It requires p(l) != 0 and p(r) != 0.
// When p is square-free the count is exact; otherwise it counts
// distinct roots.
func CountRoots(p Poly, l, r *big.Rat) int {
	seq := sturmSequence(p)
	return signVariations(seq, l) - signVariations(seq, r)
}
