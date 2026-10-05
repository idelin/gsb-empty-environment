package affarith

import "math"

// Interval is a closed real interval [Lo, Hi]. Endpoints may be infinite
// (used only for piecewise division results).
type Interval struct {
	Lo, Hi float64
}

// Contains reports whether v lies in the interval.
func (i Interval) Contains(v float64) bool { return i.Lo <= v && v <= i.Hi }

// Width returns Hi - Lo.
func (i Interval) Width() float64 { return i.Hi - i.Lo }

// Mid returns the midpoint.
func (i Interval) Mid() float64 { return (i.Lo + i.Hi) / 2 }

func iadd(a, b Interval) Interval { return Interval{a.Lo + b.Lo, a.Hi + b.Hi} }

func isub(a, b Interval) Interval { return Interval{a.Lo - b.Hi, a.Hi - b.Lo} }

func imul(a, b Interval) Interval {
	p := [4]float64{a.Lo * b.Lo, a.Lo * b.Hi, a.Hi * b.Lo, a.Hi * b.Hi}
	lo, hi := p[0], p[0]
	for _, v := range p[1:] {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	return Interval{lo, hi}
}

// idiv divides intervals. ok is false when the divisor contains zero.
func idiv(a, b Interval) (r Interval, ok bool) {
	if b.Lo <= 0 && 0 <= b.Hi {
		return Interval{}, false
	}
	q := [4]float64{a.Lo / b.Lo, a.Lo / b.Hi, a.Hi / b.Lo, a.Hi / b.Hi}
	lo, hi := q[0], q[0]
	for _, v := range q[1:] {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	return Interval{lo, hi}, true
}

// irecipClipped is the reciprocal of an interval whose zero end is treated
// as an open bound, producing an unbounded result.
func irecipClipped(b Interval) Interval {
	switch {
	case b.Lo == 0 && b.Hi > 0:
		return Interval{1 / b.Hi, math.Inf(1)}
	case b.Hi == 0 && b.Lo < 0:
		return Interval{math.Inf(-1), 1 / b.Lo}
	default:
		return Interval{1 / b.Hi, 1 / b.Lo}
	}
}

// imulOpen multiplies intervals that may be unbounded. A 0 * ±Inf product
// (NaN in IEEE arithmetic) is treated as 0, which keeps the enclosure valid
// for the clipped-division use case.
func imulOpen(a, b Interval) Interval {
	p := [4]float64{a.Lo * b.Lo, a.Lo * b.Hi, a.Hi * b.Lo, a.Hi * b.Hi}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range p {
		if math.IsNaN(v) {
			v = 0
		}
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	return Interval{lo, hi}
}
