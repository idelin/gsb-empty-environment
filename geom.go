package segsweep

import "math/big"

type segKind int

const (
	kindPoint segKind = iota // zero-length segment
	kindVertical
	kindNormal
)

// seg is the internal normalized form of an input segment.
type seg struct {
	id   int
	a, b Point // canonically oriented: a <= b lexicographically
	kind segKind

	line       string   // supporting-line key ("" for zero-length)
	la, lb, lc *big.Rat // la*x + lb*y + lc = 0, normalized (nil for points)
	slope      *big.Rat // non-nil only for kindNormal
}

func normalizeSegments(input []IntSegment) []*seg {
	segs := make([]*seg, 0, len(input))
	for _, in := range input {
		segs = append(segs, newSeg(in))
	}
	return segs
}

func newSeg(in IntSegment) *seg {
	a := IntPoint(in.X1, in.Y1)
	b := IntPoint(in.X2, in.Y2)
	if b.Cmp(a) < 0 {
		a, b = b, a
	}
	s := &seg{id: in.ID, a: a, b: b}
	switch {
	case a.Cmp(b) == 0:
		s.kind = kindPoint
		return s
	case a.X.Cmp(b.X) == 0:
		s.kind = kindVertical
	default:
		s.kind = kindNormal
	}
	// Supporting line through the (integer) endpoints, computed with
	// big.Int to avoid overflow, then normalized by gcd and sign.
	la := new(big.Int).Sub(big.NewInt(in.Y1), big.NewInt(in.Y2))
	lb := new(big.Int).Sub(big.NewInt(in.X2), big.NewInt(in.X1))
	lc := new(big.Int).Mul(big.NewInt(in.X1), big.NewInt(in.Y2))
	lc.Sub(lc, new(big.Int).Mul(big.NewInt(in.X2), big.NewInt(in.Y1)))
	g := gcd3(la, lb, lc)
	la.Quo(la, g)
	lb.Quo(lb, g)
	lc.Quo(lc, g)
	if la.Sign() < 0 || (la.Sign() == 0 && lb.Sign() < 0) {
		la.Neg(la)
		lb.Neg(lb)
		lc.Neg(lc)
	}
	s.la = new(big.Rat).SetInt(la)
	s.lb = new(big.Rat).SetInt(lb)
	s.lc = new(big.Rat).SetInt(lc)
	s.line = la.String() + ";" + lb.String() + ";" + lc.String()
	if s.kind == kindNormal {
		s.slope = new(big.Rat).Neg(new(big.Rat).Quo(s.la, s.lb))
	}
	return s
}

func gcd3(a, b, c *big.Int) *big.Int {
	ga := new(big.Int).Abs(a)
	gb := new(big.Int).Abs(b)
	gc := new(big.Int).Abs(c)
	g := new(big.Int).GCD(nil, nil, ga, gb)
	g.GCD(nil, nil, g, gc)
	if g.Sign() == 0 {
		g.SetInt64(1)
	}
	return g
}

// yAt evaluates the segment's y coordinate at x (kindNormal only).
func (s *seg) yAt(x *big.Rat) *big.Rat {
	d := new(big.Rat).Sub(x, s.a.X)
	d.Mul(d, s.slope)
	return d.Add(d, s.a.Y)
}

// contains reports whether p lies on the segment (inclusive).
func (s *seg) contains(p Point) bool {
	if s.kind == kindPoint {
		return s.a.Cmp(p) == 0
	}
	if p.X.Cmp(s.a.X) < 0 || p.X.Cmp(s.b.X) > 0 {
		return false
	}
	if s.kind == kindVertical {
		return p.Y.Cmp(s.a.Y) >= 0 && p.Y.Cmp(s.b.Y) <= 0
	}
	lo, hi := s.a.Y, s.b.Y
	if lo.Cmp(hi) > 0 {
		lo, hi = hi, lo
	}
	if p.Y.Cmp(lo) < 0 || p.Y.Cmp(hi) > 0 {
		return false
	}
	t := new(big.Rat).Mul(s.la, p.X)
	t.Add(t, new(big.Rat).Mul(s.lb, p.Y))
	t.Add(t, s.lc)
	return t.Sign() == 0
}

// lineIntersection solves the supporting lines of two non-parallel,
// non-point segments.
func lineIntersection(s1, s2 *seg) (Point, bool) {
	det := new(big.Rat).Sub(
		new(big.Rat).Mul(s1.la, s2.lb),
		new(big.Rat).Mul(s2.la, s1.lb))
	if det.Sign() == 0 {
		return Point{}, false
	}
	x := new(big.Rat).Sub(
		new(big.Rat).Mul(s1.lb, s2.lc),
		new(big.Rat).Mul(s2.lb, s1.lc))
	x.Quo(x, det)
	y := new(big.Rat).Sub(
		new(big.Rat).Mul(s2.la, s1.lc),
		new(big.Rat).Mul(s1.la, s2.lc))
	y.Quo(y, det)
	return Point{x, y}, true
}

// linePointAt maps a 1D parameter t on the supporting line of rep back
// to a 2D point. The parameter is x for non-vertical lines, y otherwise.
func linePointAt(rep *seg, t *big.Rat) Point {
	if rep.kind == kindVertical {
		x := new(big.Rat).Quo(rep.lc, rep.la)
		x.Neg(x)
		return Point{x, new(big.Rat).Set(t)}
	}
	y := new(big.Rat).Mul(rep.la, t)
	y.Add(y, rep.lc)
	y.Neg(y)
	y.Quo(y, rep.lb)
	return Point{new(big.Rat).Set(t), y}
}
