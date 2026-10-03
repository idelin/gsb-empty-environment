package sdf

import "math"

// SDF2 is a 2D signed distance field: Distance returns the signed
// Euclidean distance from p to the nearest surface. The sign is
// negative inside the geometry and positive outside.
type SDF2 interface {
	Distance(p Vec2) float64
}

// SDF2Func adapts a plain function to the SDF2 interface.
type SDF2Func func(p Vec2) float64

// Distance implements SDF2.
func (f SDF2Func) Distance(p Vec2) float64 { return f(p) }

// Circle is a 2D disc primitive.
type Circle struct {
	Center Vec2
	Radius float64
}

// Distance implements SDF2.
func (c Circle) Distance(p Vec2) float64 {
	return p.Sub(c.Center).Len() - c.Radius
}

// Box2 is an axis-aligned rectangle primitive centered at Center
// with half-extents Half.
type Box2 struct {
	Center Vec2
	Half   Vec2
}

// Distance implements SDF2.
func (b Box2) Distance(p Vec2) float64 {
	d := p.Sub(b.Center).Abs().Sub(b.Half)
	ox, oy := math.Max(d.X, 0), math.Max(d.Y, 0)
	outside := math.Hypot(ox, oy)
	inside := math.Min(math.Max(d.X, d.Y), 0)
	return outside + inside
}

// Segment2D is a capsule-style primitive around the segment A->B
// with the given radius.
type Segment2D struct {
	A, B   Vec2
	Radius float64
}

// Distance implements SDF2.
func (s Segment2D) Distance(p Vec2) float64 {
	pa := p.Sub(s.A)
	ba := s.B.Sub(s.A)
	h := math.Max(0, math.Min(1, pa.Dot(ba)/ba.LenSq()))
	return pa.Sub(ba.Scale(h)).Len() - s.Radius
}

// Union2 returns the union (min) of two 2D fields.
func Union2(a, b SDF2) SDF2 {
	return SDF2Func(func(p Vec2) float64 {
		return math.Min(a.Distance(p), b.Distance(p))
	})
}

// Intersect2 returns the intersection (max) of two 2D fields.
func Intersect2(a, b SDF2) SDF2 {
	return SDF2Func(func(p Vec2) float64 {
		return math.Max(a.Distance(p), b.Distance(p))
	})
}

// Subtract2 returns a with b carved out of it.
func Subtract2(a, b SDF2) SDF2 {
	return SDF2Func(func(p Vec2) float64 {
		return math.Max(a.Distance(p), -b.Distance(p))
	})
}

// SmoothUnion2 blends two 2D fields with smoothing radius k.
func SmoothUnion2(a, b SDF2, k float64) SDF2 {
	return SDF2Func(func(p Vec2) float64 {
		da, db := a.Distance(p), b.Distance(p)
		h := math.Max(k-math.Abs(da-db), 0) / k
		return math.Min(da, db) - h*h*k*0.25
	})
}

// Translate2 shifts a 2D field by off.
func Translate2(s SDF2, off Vec2) SDF2 {
	return SDF2Func(func(p Vec2) float64 {
		return s.Distance(p.Sub(off))
	})
}
