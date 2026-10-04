package ratbezier

import (
	"math/big"
)

// CriticalPoint is a parameter value where the curve may attain an extremum:
// either an endpoint of [0,1] or a root of the derivative polynomial.
type CriticalPoint struct {
	T      *big.Rat // exact t, or interval midpoint approximation when inexact
	TLo    *big.Rat // non-nil (with THi) when t is only isolated to an interval
	THi    *big.Rat
	Y      *big.Rat // curve value at T (exact rational)
	Kind   string   // "endpoint", "local max", "local min" or "stationary"
	Global string   // "global max", "global min", "global max+min" or ""
}

// IsExactT reports whether T is the exact abscissa of the critical point.
func (cp CriticalPoint) IsExactT() bool { return cp.TLo == nil }

// Segment is a maximal monotonic piece of the curve between two consecutive
// critical points.
type Segment struct {
	From, To *big.Rat
	Dir      string // "increasing", "decreasing" or "constant"
}

// Analysis is the full extremum report for a curve on [0,1].
type Analysis struct {
	Numerator   *Poly // N(t)
	Denominator *Poly // D(t)
	Derivative  *Poly // P(t) = N'D - ND'
	Critical    []CriticalPoint
	Segments    []Segment
}

// Max returns the critical points tagged as global maxima.
func (a *Analysis) Max() (y *big.Rat, pts []CriticalPoint) {
	return a.extremum("global max")
}

// Min returns the critical points tagged as global minima.
func (a *Analysis) Min() (y *big.Rat, pts []CriticalPoint) {
	return a.extremum("global min")
}

func (a *Analysis) extremum(kind string) (*big.Rat, []CriticalPoint) {
	var pts []CriticalPoint
	for _, cp := range a.Critical {
		if cp.Global == kind || cp.Global == "global max+min" {
			pts = append(pts, cp)
		}
	}
	if len(pts) == 0 {
		return nil, nil
	}
	return pts[0].Y, pts
}

// Analyze locates every extremum of the curve on [0,1]: it derives the
// integer-coefficient derivative polynomial, isolates all of its
// sign-changing roots in [0,1], determines the monotonic segments, and
// evaluates the curve exactly at every critical point (endpoints included).
func (c *Curve) Analyze() *Analysis {
	n := c.NumeratorPoly()
	d := c.DenominatorPoly()
	p := n.Derivative().Mul(d).Add(n.Mul(d.Derivative()).Neg())

	zero, one := rat(0), rat(1)
	a := &Analysis{Numerator: n, Denominator: d, Derivative: p}

	// Critical points: t = 0, interior roots, t = 1 (roots come out sorted).
	a.Critical = append(a.Critical, c.criticalAt(Root{Exact: zero, Lo: zero, Hi: zero}, "endpoint"))
	for _, r := range IsolateRoots(p) {
		t := r.Approx()
		if t.Cmp(zero) == 0 || t.Cmp(one) == 0 {
			continue // endpoints already covered
		}
		a.Critical = append(a.Critical, c.criticalAt(r, ""))
	}
	a.Critical = append(a.Critical, c.criticalAt(Root{Exact: one, Lo: one, Hi: one}, "endpoint"))

	// Monotonic segments between consecutive critical points.
	half := big.NewRat(1, 2)
	for i := 0; i+1 < len(a.Critical); i++ {
		from := a.Critical[i].T
		to := a.Critical[i+1].T
		mid := new(big.Rat).Add(from, to)
		mid.Mul(mid, half)
		dir := "constant"
		switch p.Sign(mid) {
		case 1:
			dir = "increasing"
		case -1:
			dir = "decreasing"
		}
		a.Segments = append(a.Segments, Segment{From: from, To: to, Dir: dir})
	}

	// Classify interior critical points from neighbouring segment directions.
	for i := 1; i+1 < len(a.Critical); i++ {
		left, right := a.Segments[i-1].Dir, a.Segments[i].Dir
		switch {
		case left == "increasing" && right == "decreasing":
			a.Critical[i].Kind = "local max"
		case left == "decreasing" && right == "increasing":
			a.Critical[i].Kind = "local min"
		default:
			a.Critical[i].Kind = "stationary"
		}
	}

	// Tag global extrema by exact comparison of the rational values.
	if len(a.Critical) > 0 {
		maxY, minY := a.Critical[0].Y, a.Critical[0].Y
		for _, cp := range a.Critical[1:] {
			if cp.Y.Cmp(maxY) > 0 {
				maxY = cp.Y
			}
			if cp.Y.Cmp(minY) < 0 {
				minY = cp.Y
			}
		}
		for i := range a.Critical {
			isMax := a.Critical[i].Y.Cmp(maxY) == 0
			isMin := a.Critical[i].Y.Cmp(minY) == 0
			switch {
			case isMax && isMin:
				a.Critical[i].Global = "global max+min"
			case isMax:
				a.Critical[i].Global = "global max"
			case isMin:
				a.Critical[i].Global = "global min"
			}
		}
	}
	return a
}

func (c *Curve) criticalAt(r Root, kind string) CriticalPoint {
	t := r.Approx()
	cp := CriticalPoint{T: t, Y: c.Eval(t), Kind: kind}
	if !r.IsExact() {
		cp.TLo = r.Lo
		cp.THi = r.Hi
	}
	return cp
}
