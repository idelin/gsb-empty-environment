package ratbezier

import "math/big"

// sturmSeq is a Sturm sequence of rational polynomials used to count
// distinct real roots of an integer polynomial in an interval.
type sturmSeq []*ratPoly

func buildSturmSeq(p *Poly) sturmSeq {
	f := ratPolyFromPoly(p)
	seq := sturmSeq{f, f.deriv()}
	for {
		n := len(seq)
		r := seq[n-2].rem(seq[n-1]).neg()
		if r.isZero() {
			break
		}
		seq = append(seq, r)
	}
	return seq
}

// variations counts sign changes of the sequence evaluated at x,
// skipping zero entries (standard Sturm convention).
func (s sturmSeq) variations(x *big.Rat) int {
	count := 0
	prev := 0
	for _, q := range s {
		v := q.eval(x).Sign()
		if v == 0 {
			continue
		}
		if prev != 0 && v != prev {
			count++
		}
		prev = v
	}
	return count
}

// countRoots returns the number of distinct real roots in (a, b].
func (s sturmSeq) countRoots(a, b *big.Rat) int {
	return s.variations(a) - s.variations(b)
}
