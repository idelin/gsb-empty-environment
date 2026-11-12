package taskpkg

import "math"

// Vec3 is a 3D vector used for positions, directions and bounds corners.
type Vec3 struct {
	X, Y, Z float64
}

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }

func (a Vec3) Dot(b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

// Lerp linearly interpolates between a and b at parameter t.
func (a Vec3) Lerp(b Vec3, t float64) Vec3 {
	return Vec3{
		a.X + (b.X-a.X)*t,
		a.Y + (b.Y-a.Y)*t,
		a.Z + (b.Z-a.Z)*t,
	}
}

// Component returns the i-th component (0=x, 1=y, 2=z).
func (a Vec3) Component(i int) float64 {
	switch i {
	case 0:
		return a.X
	case 1:
		return a.Y
	default:
		return a.Z
	}
}

func (a Vec3) min(b Vec3) Vec3 {
	return Vec3{math.Min(a.X, b.X), math.Min(a.Y, b.Y), math.Min(a.Z, b.Z)}
}

func (a Vec3) max(b Vec3) Vec3 {
	return Vec3{math.Max(a.X, b.X), math.Max(a.Y, b.Y), math.Max(a.Z, b.Z)}
}

// AABB is an axis-aligned bounding box.
type AABB struct {
	Min, Max Vec3
}

func emptyAABB() AABB {
	inf := math.Inf(1)
	return AABB{Min: Vec3{inf, inf, inf}, Max: Vec3{-inf, -inf, -inf}}
}

// Union returns the smallest box containing both a and b.
func (a AABB) Union(b AABB) AABB {
	return AABB{Min: a.Min.min(b.Min), Max: a.Max.max(b.Max)}
}

// Extend grows the box to include p.
func (a AABB) Extend(p Vec3) AABB {
	return AABB{Min: a.Min.min(p), Max: a.Max.max(p)}
}

// Center returns the box center.
func (a AABB) Center() Vec3 {
	return Vec3{(a.Min.X + a.Max.X) * 0.5, (a.Min.Y + a.Max.Y) * 0.5, (a.Min.Z + a.Max.Z) * 0.5}
}

// SurfaceArea returns the surface area of the box.
func (a AABB) SurfaceArea() float64 {
	dx := a.Max.X - a.Min.X
	dy := a.Max.Y - a.Min.Y
	dz := a.Max.Z - a.Min.Z
	return 2 * (dx*dy + dy*dz + dz*dx)
}

// MaxSpanAxis returns the axis with the largest extent.
func (a AABB) MaxSpanAxis() int {
	dx := a.Max.X - a.Min.X
	dy := a.Max.Y - a.Min.Y
	dz := a.Max.Z - a.Min.Z
	if dx >= dy && dx >= dz {
		return 0
	}
	if dy >= dz {
		return 1
	}
	return 2
}

// hit reports whether the ray (origin, precomputed inverse direction) hits
// the box at a distance below tMax.
func (a AABB) hit(orig, invDir Vec3, tMax float64) bool {
	t0 := (a.Min.X - orig.X) * invDir.X
	t1 := (a.Max.X - orig.X) * invDir.X
	tmin := math.Min(t0, t1)
	tmax := math.Max(t0, t1)

	t0 = (a.Min.Y - orig.Y) * invDir.Y
	t1 = (a.Max.Y - orig.Y) * invDir.Y
	tmin = math.Max(tmin, math.Min(t0, t1))
	tmax = math.Min(tmax, math.Max(t0, t1))

	t0 = (a.Min.Z - orig.Z) * invDir.Z
	t1 = (a.Max.Z - orig.Z) * invDir.Z
	tmin = math.Max(tmin, math.Min(t0, t1))
	tmax = math.Min(tmax, math.Max(t0, t1))

	return tmax >= math.Max(tmin, 0) && tmin < tMax
}
