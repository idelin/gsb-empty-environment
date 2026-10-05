package trigcert

import "math/big"

// Interval is a closed interval with rational endpoints.
// It is a value type; all operations return new intervals.
type Interval struct {
	Lo, Hi *big.Rat
}

// NewInterval returns [lo, hi], swapping the endpoints if necessary.
func NewInterval(lo, hi *big.Rat) Interval {
	l := new(big.Rat).Set(lo)
	h := new(big.Rat).Set(hi)
	if l.Cmp(h) > 0 {
		l, h = h, l
	}
	return Interval{Lo: l, Hi: h}
}

// Point returns the degenerate interval [v, v].
func Point(v *big.Rat) Interval {
	return Interval{Lo: new(big.Rat).Set(v), Hi: new(big.Rat).Set(v)}
}

// Add returns i + j.
func (i Interval) Add(j Interval) Interval {
	return Interval{
		Lo: new(big.Rat).Add(i.Lo, j.Lo),
		Hi: new(big.Rat).Add(i.Hi, j.Hi),
	}
}

// Sub returns i - j.
func (i Interval) Sub(j Interval) Interval {
	return Interval{
		Lo: new(big.Rat).Sub(i.Lo, j.Hi),
		Hi: new(big.Rat).Sub(i.Hi, j.Lo),
	}
}

// Neg returns -i.
func (i Interval) Neg() Interval {
	return Interval{
		Lo: new(big.Rat).Neg(i.Hi),
		Hi: new(big.Rat).Neg(i.Lo),
	}
}

// Mul returns i * j.
func (i Interval) Mul(j Interval) Interval {
	p1 := new(big.Rat).Mul(i.Lo, j.Lo)
	p2 := new(big.Rat).Mul(i.Lo, j.Hi)
	p3 := new(big.Rat).Mul(i.Hi, j.Lo)
	p4 := new(big.Rat).Mul(i.Hi, j.Hi)
	lo, hi := p1, p1
	for _, p := range []*big.Rat{p2, p3, p4} {
		if p.Cmp(lo) < 0 {
			lo = p
		}
		if p.Cmp(hi) > 0 {
			hi = p
		}
	}
	return Interval{Lo: lo, Hi: hi}
}

// Scale returns i multiplied by the rational s.
func (i Interval) Scale(s *big.Rat) Interval {
	lo := new(big.Rat).Mul(i.Lo, s)
	hi := new(big.Rat).Mul(i.Hi, s)
	if s.Sign() < 0 {
		lo, hi = hi, lo
	}
	return Interval{Lo: lo, Hi: hi}
}

// ScaleInt returns i multiplied by the integer k.
func (i Interval) ScaleInt(k *big.Int) Interval {
	return i.Scale(new(big.Rat).SetInt(k))
}

// Width returns Hi - Lo (non-negative).
func (i Interval) Width() *big.Rat {
	return new(big.Rat).Sub(i.Hi, i.Lo)
}

// Mid returns the exact rational midpoint (Lo + Hi) / 2.
func (i Interval) Mid() *big.Rat {
	m := new(big.Rat).Add(i.Lo, i.Hi)
	return m.Quo(m, big.NewRat(2, 1))
}

// Expand returns [Lo - r, Hi + r]; r must be non-negative.
func (i Interval) Expand(r *big.Rat) Interval {
	return Interval{
		Lo: new(big.Rat).Sub(i.Lo, r),
		Hi: new(big.Rat).Add(i.Hi, r),
	}
}

// Contains reports whether v lies in i.
func (i Interval) Contains(v *big.Rat) bool {
	return i.Lo.Cmp(v) <= 0 && v.Cmp(i.Hi) <= 0
}

// Overlaps reports whether i and j share at least one point.
func (i Interval) Overlaps(j Interval) bool {
	return i.Lo.Cmp(j.Hi) <= 0 && j.Lo.Cmp(i.Hi) <= 0
}

// Intersect returns the intersection of i and j. If the intervals are
// disjoint it returns the empty gap between them collapsed to the nearer
// endpoint; callers in this package only intersect overlapping intervals.
func (i Interval) Intersect(j Interval) Interval {
	lo := i.Lo
	if j.Lo.Cmp(lo) > 0 {
		lo = j.Lo
	}
	hi := i.Hi
	if j.Hi.Cmp(hi) < 0 {
		hi = j.Hi
	}
	if lo.Cmp(hi) > 0 {
		m := new(big.Rat).Add(lo, hi)
		m.Quo(m, big.NewRat(2, 1))
		return Point(m)
	}
	return Interval{Lo: new(big.Rat).Set(lo), Hi: new(big.Rat).Set(hi)}
}
