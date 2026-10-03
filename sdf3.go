package sdf

import "math"

// SDF3 is a 3D signed distance field: Distance returns the signed
// Euclidean distance from p to the nearest surface. The sign is
// negative inside the geometry and positive outside.
type SDF3 interface {
	Distance(p Vec3) float64
}

// SDF3Func adapts a plain function to the SDF3 interface.
type SDF3Func func(p Vec3) float64

// Distance implements SDF3.
func (f SDF3Func) Distance(p Vec3) float64 { return f(p) }

// Sphere is a 3D sphere primitive.
type Sphere struct {
	Center Vec3
	Radius float64
}

// Distance implements SDF3.
func (s Sphere) Distance(p Vec3) float64 {
	return p.Sub(s.Center).Len() - s.Radius
}

// Box3 is an axis-aligned box primitive centered at Center with
// half-extents Half.
type Box3 struct {
	Center Vec3
	Half   Vec3
}

// Distance implements SDF3.
func (b Box3) Distance(p Vec3) float64 {
	d := p.Sub(b.Center).Abs().Sub(b.Half)
	ox := math.Max(d.X, 0)
	oy := math.Max(d.Y, 0)
	oz := math.Max(d.Z, 0)
	outside := Vec3{ox, oy, oz}.Len()
	inside := math.Min(math.Max(d.X, math.Max(d.Y, d.Z)), 0)
	return outside + inside
}

// Plane3 is an infinite plane through Point with unit Normal.
type Plane3 struct {
	Point  Vec3
	Normal Vec3
}

// Distance implements SDF3.
func (pl Plane3) Distance(p Vec3) float64 {
	return p.Sub(pl.Point).Dot(pl.Normal)
}

// Cylinder is a cylinder around the Y axis through Center with the
// given radius and half-height.
type Cylinder struct {
	Center     Vec3
	Radius     float64
	HalfHeight float64
}

// Distance implements SDF3.
func (c Cylinder) Distance(p Vec3) float64 {
	q := p.Sub(c.Center)
	dx := math.Hypot(q.X, q.Z) - c.Radius
	dy := math.Abs(q.Y) - c.HalfHeight
	outside := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	inside := math.Min(math.Max(dx, dy), 0)
	return outside + inside
}

// Union3 returns the union (min) of two 3D fields.
func Union3(a, b SDF3) SDF3 {
	return SDF3Func(func(p Vec3) float64 {
		return math.Min(a.Distance(p), b.Distance(p))
	})
}

// Intersect3 returns the intersection (max) of two 3D fields.
func Intersect3(a, b SDF3) SDF3 {
	return SDF3Func(func(p Vec3) float64 {
		return math.Max(a.Distance(p), b.Distance(p))
	})
}

// Subtract3 returns a with b carved out of it.
func Subtract3(a, b SDF3) SDF3 {
	return SDF3Func(func(p Vec3) float64 {
		return math.Max(a.Distance(p), -b.Distance(p))
	})
}

// SmoothUnion3 blends two 3D fields with smoothing radius k.
func SmoothUnion3(a, b SDF3, k float64) SDF3 {
	return SDF3Func(func(p Vec3) float64 {
		da, db := a.Distance(p), b.Distance(p)
		h := math.Max(k-math.Abs(da-db), 0) / k
		return math.Min(da, db) - h*h*k*0.25
	})
}

// Translate3 shifts a 3D field by off.
func Translate3(s SDF3, off Vec3) SDF3 {
	return SDF3Func(func(p Vec3) float64 {
		return s.Distance(p.Sub(off))
	})
}
