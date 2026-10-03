package sdf

import "math"

// SDF2 evaluates the signed distance (negative inside, positive outside)
// from a 2D point to the nearest surface of a shape.
type SDF2 func(p Vec2) float64

// SDF3 evaluates the signed distance (negative inside, positive outside)
// from a 3D point to the nearest surface of a shape.
type SDF3 func(p Vec3) float64

// Circle2 returns the SDF of a circle centered at c with radius r.
func Circle2(c Vec2, r float64) SDF2 {
	return func(p Vec2) float64 {
		return p.Sub(c).Len() - r
	}
}

// Box2 returns the SDF of an axis-aligned box centered at c with
// half-extents b.
func Box2(c, b Vec2) SDF2 {
	return func(p Vec2) float64 {
		d := p.Sub(c)
		qx := abs2(d.X) - b.X
		qy := abs2(d.Y) - b.Y
		ax, ay := maxf(qx, 0), maxf(qy, 0)
		return math.Hypot(ax, ay) + minf(maxf(qx, qy), 0)
	}
}

// Segment2 returns the SDF of the capsule around segment a-b with radius r.
func Segment2(a, b Vec2, r float64) SDF2 {
	return func(p Vec2) float64 {
		pa := p.Sub(a)
		ba := b.Sub(a)
		denom := ba.Dot(ba)
		h := 0.0
		if denom > 0 {
			h = minf(maxf(pa.Dot(ba)/denom, 0), 1)
		}
		return pa.Sub(ba.Mul(h)).Len() - r
	}
}

// Sphere returns the SDF of a sphere centered at c with radius r.
func Sphere(c Vec3, r float64) SDF3 {
	return func(p Vec3) float64 {
		return p.Sub(c).Len() - r
	}
}

// Box returns the SDF of an axis-aligned box centered at c with
// half-extents b.
func Box(c, b Vec3) SDF3 {
	return func(p Vec3) float64 {
		d := p.Sub(c)
		qx := abs3(d.X) - b.X
		qy := abs3(d.Y) - b.Y
		qz := abs3(d.Z) - b.Z
		ex := maxf(qx, 0)
		ey := maxf(qy, 0)
		ez := maxf(qz, 0)
		outside := math.Sqrt(ex*ex + ey*ey + ez*ez)
		inside := minf(maxf(qx, maxf(qy, qz)), 0)
		return outside + inside
	}
}

// Plane returns the SDF of a plane through point p0 with unit normal n
// (positive on the side the normal points to).
func Plane(p0, n Vec3) SDF3 {
	n = n.Norm()
	return func(p Vec3) float64 {
		return p.Sub(p0).Dot(n)
	}
}

// Union2 combines 2D shapes (CSG union).
func Union2(shapes ...SDF2) SDF2 {
	return func(p Vec2) float64 {
		d := math.Inf(1)
		for _, s := range shapes {
			d = minf(d, s(p))
		}
		return d
	}
}

// Union combines 3D shapes (CSG union).
func Union(shapes ...SDF3) SDF3 {
	return func(p Vec3) float64 {
		d := math.Inf(1)
		for _, s := range shapes {
			d = minf(d, s(p))
		}
		return d
	}
}

// Subtract2 subtracts shape b from shape a (CSG difference).
func Subtract2(a, b SDF2) SDF2 {
	return func(p Vec2) float64 {
		return maxf(a(p), -b(p))
	}
}

// Subtract subtracts shape b from shape a (CSG difference).
func Subtract(a, b SDF3) SDF3 {
	return func(p Vec3) float64 {
		return maxf(a(p), -b(p))
	}
}
