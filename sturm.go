package ratbez

import "math/big"

// Sturm is the Sturm sequence of a square-free polynomial, used to count
// distinct real roots in an interval exactly.
type Sturm struct{ seq []Poly }

// NewSturm builds the Sturm sequence of p. For correct root counts p must be
// square-free (use SquareFree first if needed).
func NewSturm(p Poly) *Sturm {
	seq := []Poly{p, p.Deriv()}
	for {
		n := len(seq)
		if seq[n-1].IsZero() {
			break
		}
		_, r := QuoRem(seq[n-2], seq[n-1])
		r = Neg(r)
		if r.IsZero() {
			break
		}
		seq = append(seq, r)
	}
	return &Sturm{seq: seq}
}

// variations counts sign changes of the Sturm sequence at x, skipping zeros.
func (s *Sturm) variations(x *big.Rat) int {
	prev, count := 0, 0
	for _, f := range s.seq {
		sg := f.Eval(x).Sign()
		if sg == 0 {
			continue
		}
		if prev != 0 && sg != prev {
			count++
		}
		prev = sg
	}
	return count
}

// CountOpen counts the distinct real roots of the polynomial in the open
// interval (a, b). By Sturm's theorem, V(a)-V(b) counts roots in (a, b]
// (a root exactly at a is excluded by the zero-skipping convention), so we
// subtract a possible root exactly at b.
func (s *Sturm) CountOpen(a, b *big.Rat) int {
	c := s.variations(a) - s.variations(b)
	if s.seq[0].Eval(b).Sign() == 0 {
		c--
	}
	return c
}
