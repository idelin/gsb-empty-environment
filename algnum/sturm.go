package algnum

import "math/big"

// SturmSeq is the Sturm sequence of a square-free polynomial, used to
// count distinct real roots in an interval exactly.
type SturmSeq struct {
	base Poly   // the square-free polynomial the sequence was built from
	seq  []Poly // seq[0] = base, seq[1] = base', then negative remainders
}

// NewSturmSeq builds the Sturm sequence of p. p must be square-free and
// non-constant (use Poly.SquareFree first if needed).
func NewSturmSeq(p Poly) SturmSeq {
	if p.Deg() < 1 {
		return SturmSeq{base: p}
	}
	seq := []Poly{p, p.Deriv()}
	for {
		_, r := seq[len(seq)-2].DivMod(seq[len(seq)-1])
		r = r.Neg()
		if r.IsZero() {
			break
		}
		seq = append(seq, r)
	}
	return SturmSeq{base: p, seq: seq}
}

// variations counts sign changes of the Sturm sequence evaluated at x,
// skipping zero values (the standard convention of Sturm's theorem).
func (s SturmSeq) variations(x *big.Rat) int {
	v := 0
	prev := 0
	for _, f := range s.seq {
		sg := f.SignAt(x)
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

// CountRoots returns the number of distinct real roots of the base
// polynomial in the open interval (lo, hi). It requires lo < hi and that
// neither endpoint is a root of the base polynomial.
func (s SturmSeq) CountRoots(lo, hi *big.Rat) int {
	if len(s.seq) == 0 {
		return 0
	}
	return s.variations(lo) - s.variations(hi)
}
