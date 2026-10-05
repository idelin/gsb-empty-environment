package segint

import "math/big"

type pairKind int

const (
	pairNone pairKind = iota
	pairPoint
	pairOverlap
)

// pairIntersection is the exact intersection of two segments: empty, a
// single point, or a positive-length collinear overlap [lo, hi].
type pairIntersection struct {
	kind   pairKind
	point  Point // pairPoint
	lo, hi Point // pairOverlap, ordered canonically along the line
}

func ratSub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }

// cross2 returns the z-component of (ax,ay) x (bx,by).
func cross2(ax, ay, bx, by *big.Rat) *big.Rat {
	t := new(big.Rat).Mul(ax, by)
	return t.Sub(t, new(big.Rat).Mul(ay, bx))
}

func inUnit(t *big.Rat) bool {
	return t.Sign() >= 0 && t.Cmp(big.NewRat(1, 1)) <= 0
}

// pointOnSegment reports whether p lies on the non-degenerate segment s
// (inclusive of endpoints).
func pointOnSegment(p Point, s Segment) bool {
	dx := ratSub(s.P2.X, s.P1.X)
	dy := ratSub(s.P2.Y, s.P1.Y)
	rx := ratSub(p.X, s.P1.X)
	ry := ratSub(p.Y, s.P1.Y)
	if cross2(dx, dy, rx, ry).Sign() != 0 {
		return false
	}
	return between(p.X, s.P1.X, s.P2.X) && between(p.Y, s.P1.Y, s.P2.Y)
}

func between(v, a, b *big.Rat) bool {
	if a.Cmp(b) > 0 {
		a, b = b, a
	}
	return v.Cmp(a) >= 0 && v.Cmp(b) <= 0
}

func pairIntersect(a, b Segment) pairIntersection {
	aDeg, bDeg := a.degenerate(), b.degenerate()
	switch {
	case aDeg && bDeg:
		if pointEqual(a.P1, b.P1) {
			return pairIntersection{kind: pairPoint, point: a.P1}
		}
		return pairIntersection{}
	case aDeg:
		if pointOnSegment(a.P1, b) {
			return pairIntersection{kind: pairPoint, point: a.P1}
		}
		return pairIntersection{}
	case bDeg:
		if pointOnSegment(b.P1, a) {
			return pairIntersection{kind: pairPoint, point: b.P1}
		}
		return pairIntersection{}
	}

	d1x := ratSub(a.P2.X, a.P1.X)
	d1y := ratSub(a.P2.Y, a.P1.Y)
	d2x := ratSub(b.P2.X, b.P1.X)
	d2y := ratSub(b.P2.Y, b.P1.Y)
	rx := ratSub(b.P1.X, a.P1.X)
	ry := ratSub(b.P1.Y, a.P1.Y)

	denom := cross2(d1x, d1y, d2x, d2y)
	if denom.Sign() != 0 {
		// Non-parallel: unique candidate intersection point.
		t := new(big.Rat).Quo(cross2(rx, ry, d2x, d2y), denom)
		u := new(big.Rat).Quo(cross2(rx, ry, d1x, d1y), denom)
		if inUnit(t) && inUnit(u) {
			px := new(big.Rat).Add(a.P1.X, new(big.Rat).Mul(t, d1x))
			py := new(big.Rat).Add(a.P1.Y, new(big.Rat).Mul(t, d1y))
			return pairIntersection{kind: pairPoint, point: Point{X: px, Y: py}}
		}
		return pairIntersection{}
	}

	// Parallel: reject if not collinear.
	if cross2(rx, ry, d1x, d1y).Sign() != 0 {
		return pairIntersection{}
	}

	// Collinear: intersect the 1D parameter intervals. The parameter is x
	// for non-vertical lines, y for vertical ones.
	useX := d1x.Sign() != 0
	param := func(p Point) *big.Rat {
		if useX {
			return p.X
		}
		return p.Y
	}
	alo, ahi := param(a.P1), param(a.P2)
	if alo.Cmp(ahi) > 0 {
		alo, ahi = ahi, alo
	}
	blo, bhi := param(b.P1), param(b.P2)
	if blo.Cmp(bhi) > 0 {
		blo, bhi = bhi, blo
	}
	lo := alo
	if blo.Cmp(lo) > 0 {
		lo = blo
	}
	hi := ahi
	if bhi.Cmp(hi) < 0 {
		hi = bhi
	}
	switch lo.Cmp(hi) {
	case 1:
		return pairIntersection{}
	case 0:
		// Single shared endpoint: an endpoint of one of the segments.
		for _, p := range []Point{a.P1, a.P2, b.P1, b.P2} {
			if param(p).Cmp(lo) == 0 {
				return pairIntersection{kind: pairPoint, point: p}
			}
		}
		return pairIntersection{}
	}
	// Positive-length overlap: its ends are endpoints of the inputs.
	var loP, hiP Point
	for _, p := range []Point{a.P1, a.P2, b.P1, b.P2} {
		switch param(p).Cmp(lo) {
		case 0:
			loP = p
		}
		switch param(p).Cmp(hi) {
		case 0:
			hiP = p
		}
	}
	return pairIntersection{kind: pairOverlap, lo: loP, hi: hiP}
}
