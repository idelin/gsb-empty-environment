package rootfind

import "math/big"

// sturmSeq is a Sturm sequence of a square-free polynomial:
// s0 = f, s1 = f', s(i+1) = -rem(s(i-1), s(i)).
type sturmSeq []Poly

// buildSturm constructs the Sturm sequence of f. f must be square-free
// (gcd(f, f') = 1), which holds for the factors produced by
// squareFreeFactors; then the last sequence element is a nonzero constant.
func buildSturm(f Poly) sturmSeq {
	seq := sturmSeq{f, f.Deriv()}
	for {
		_, r := DivMod(seq[len(seq)-2], seq[len(seq)-1])
		if r.IsZero() {
			break
		}
		seq = append(seq, Neg(r))
	}
	return seq
}

// variations counts sign changes in the sequence evaluated at x,
// skipping zero values (exact rational arithmetic, no sampling error).
func (s sturmSeq) variations(x *big.Rat) int {
	prev := 0
	count := 0
	for _, p := range s {
		sign := p.Eval(x).Sign()
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

// countRoots returns the exact number of distinct real roots of the
// square-free polynomial in the half-open interval (a, b], by Sturm's
// theorem: V(a) - V(b). A root exactly at a is NOT counted; a root
// exactly at b IS counted.
func (s sturmSeq) countRoots(a, b *big.Rat) int {
	return s.variations(a) - s.variations(b)
}
