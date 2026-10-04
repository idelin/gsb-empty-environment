// Package piecewise computes with piecewise-polynomial functions whose
// breakpoints and coefficients are exact rationals.
//
// A Function is a sequence of segments. Each segment carries a polynomial
// valid on its open interval (Lo, Hi); the point values at Lo and Hi may be
// overridden independently, so left and right values at a breakpoint can
// differ. Integration is performed segment by segment, so endpoints are
// never counted twice and point values never contribute (impulse /
// distributional terms are out of scope). Functions are immutable:
// Replace returns a new version while the old one stays valid.
package piecewise

import (
	"errors"
	"math/big"
)

// ErrEmptyInterval is returned by Replace when lo >= hi.
var ErrEmptyInterval = errors.New("piecewise: interval must satisfy lo < hi")

// Segment is one polynomial piece of a Function.
// Lo and Hi are rational breakpoints with Lo < Hi.
// LoVal/HiVal optionally override the point values at Lo/Hi;
// nil means the polynomial limit is used.
type Segment struct {
	Lo, Hi *big.Rat
	P      Poly
	LoVal  *big.Rat
	HiVal  *big.Rat
}

// Function is an immutable piecewise-polynomial function.
type Function struct {
	segs []Segment // sorted by Lo, non-overlapping, gaps allowed
}

// New returns an empty function (undefined everywhere).
func New() *Function { return &Function{} }

// Segments returns a copy of the segment list.
func (f *Function) Segments() []Segment {
	out := make([]Segment, len(f.segs))
	copy(out, f.segs)
	return out
}

func copyRat(x *big.Rat) *big.Rat { return new(big.Rat).Set(x) }

func copyRatOrNil(x *big.Rat) *big.Rat {
	if x == nil {
		return nil
	}
	return copyRat(x)
}

// Replace returns a new Function that equals f outside [lo, hi) and equals
// the polynomial p on (lo, hi). loVal/hiVal optionally set the point values
// at lo and hi (nil = polynomial limit). Existing segments overlapping the
// interval are cut at lo and hi; their parts outside survive. The receiver
// is not modified, so old versions remain usable.
func (f *Function) Replace(lo, hi *big.Rat, p Poly, loVal, hiVal *big.Rat) (*Function, error) {
	if lo.Cmp(hi) >= 0 {
		return nil, ErrEmptyInterval
	}
	out := &Function{}
	for _, s := range f.segs {
		if s.Hi.Cmp(lo) <= 0 || s.Lo.Cmp(hi) >= 0 {
			out.segs = append(out.segs, s) // disjoint: share as-is
			continue
		}
		if s.Lo.Cmp(lo) < 0 {
			out.segs = append(out.segs, Segment{Lo: s.Lo, Hi: copyRat(lo), P: s.P, LoVal: s.LoVal})
		}
		if s.Hi.Cmp(hi) > 0 {
			out.segs = append(out.segs, Segment{Lo: copyRat(hi), Hi: s.Hi, P: s.P, HiVal: s.HiVal})
		}
	}
	seg := Segment{Lo: copyRat(lo), Hi: copyRat(hi), P: p, LoVal: copyRatOrNil(loVal), HiVal: copyRatOrNil(hiVal)}
	i := 0
	for i < len(out.segs) && out.segs[i].Hi.Cmp(lo) <= 0 {
		i++
	}
	out.segs = append(out.segs, Segment{})
	copy(out.segs[i+1:], out.segs[i:])
	out.segs[i] = seg
	return out, nil
}

// Integrate returns the exact integral of f over [a, b], computed as the
// sum of per-segment polynomial integrals. Reversed bounds negate the
// result; a == b gives zero. Point values at breakpoints contribute
// nothing, so endpoints are never counted twice.
func (f *Function) Integrate(a, b *big.Rat) *big.Rat {
	switch a.Cmp(b) {
	case 0:
		return new(big.Rat)
	case 1:
		return new(big.Rat).Neg(f.Integrate(b, a))
	}
	total := new(big.Rat)
	for _, s := range f.segs {
		if s.Hi.Cmp(a) <= 0 {
			continue
		}
		if s.Lo.Cmp(b) >= 0 {
			break
		}
		lo, hi := s.Lo, s.Hi
		if lo.Cmp(a) < 0 {
			lo = a
		}
		if hi.Cmp(b) > 0 {
			hi = b
		}
		if lo.Cmp(hi) < 0 {
			total.Add(total, s.P.Integrate(lo, hi))
		}
	}
	return total
}

// ValueAt returns f(x). At a breakpoint the right segment's value wins
// over the left segment's HiVal. The second result is false when x lies
// outside every segment.
func (f *Function) ValueAt(x *big.Rat) (*big.Rat, bool) {
	var candidate *big.Rat
	for _, s := range f.segs {
		if x.Cmp(s.Lo) < 0 {
			break
		}
		switch {
		case x.Cmp(s.Lo) == 0:
			if s.LoVal != nil {
				return copyRat(s.LoVal), true
			}
			return s.P.Eval(x), true
		case x.Cmp(s.Hi) < 0:
			return s.P.Eval(x), true
		default: // x == s.Hi; a later segment may start at x and win
			if s.HiVal != nil {
				candidate = copyRat(s.HiVal)
			} else {
				candidate = s.P.Eval(x)
			}
		}
	}
	if candidate != nil {
		return candidate, true
	}
	return nil, false
}

// Derivative describes the derivative of a Function at one point.
type Derivative struct {
	// Left and Right are the one-sided derivatives at the point, or nil
	// when no segment reaches the point from that side.
	Left, Right *big.Rat
	// Continuous reports whether the function itself is continuous there.
	Continuous bool
	// Exists is true when the ordinary two-sided derivative exists
	// (continuous, and Left == Right); Value then holds it.
	Exists bool
	Value  *big.Rat
}

// DerivativeAt examines f at x, including breakpoints where left and right
// pieces meet or where the function jumps.
func (f *Function) DerivativeAt(x *big.Rat) Derivative {
	var d Derivative
	var leftLim, rightLim *big.Rat
	for _, s := range f.segs {
		if x.Cmp(s.Lo) < 0 {
			break
		}
		if x.Cmp(s.Lo) > 0 && x.Cmp(s.Hi) < 0 {
			v := s.P.Deriv().Eval(x)
			return Derivative{Left: v, Right: copyRat(v), Continuous: true, Exists: true, Value: copyRat(v)}
		}
		if x.Cmp(s.Hi) == 0 {
			d.Left = s.P.Deriv().Eval(x)
			leftLim = s.P.Eval(x)
		}
		if x.Cmp(s.Lo) == 0 {
			d.Right = s.P.Deriv().Eval(x)
			rightLim = s.P.Eval(x)
		}
	}
	if d.Left == nil || d.Right == nil {
		return d
	}
	d.Continuous = leftLim.Cmp(rightLim) == 0
	if v, ok := f.ValueAt(x); !ok || v.Cmp(leftLim) != 0 {
		d.Continuous = false
	}
	if d.Continuous && d.Left.Cmp(d.Right) == 0 {
		d.Exists = true
		d.Value = copyRat(d.Left)
	}
	return d
}

// Deriv returns the piecewise derivative of f: each segment's polynomial is
// replaced by its formal derivative. Point values are dropped, and the
// result is undefined at the old breakpoints (query those with
// DerivativeAt instead).
func (f *Function) Deriv() *Function {
	out := &Function{segs: make([]Segment, len(f.segs))}
	for i, s := range f.segs {
		out.segs[i] = Segment{Lo: s.Lo, Hi: s.Hi, P: s.P.Deriv()}
	}
	return out
}
