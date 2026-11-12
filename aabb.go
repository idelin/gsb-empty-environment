package taskpkg

import "math"

// AABB is an axis-aligned bounding box. The empty box has Min components
// greater than Max components.
type AABB struct {
	Min, Max Vec3
}

func EmptyAABB() AABB {
	inf := math.Inf(1)
	return AABB{
		Min: Vec3{X: inf, Y: inf, Z: inf},
		Max: Vec3{X: -inf, Y: -inf, Z: -inf},
	}
}

func (b AABB) IsEmpty() bool {
	return b.Min.X > b.Max.X || b.Min.Y > b.Max.Y || b.Min.Z > b.Max.Z
}

func (b AABB) Center() Vec3 {
	return Vec3{
		X: (b.Min.X + b.Max.X) * 0.5,
		Y: (b.Min.Y + b.Max.Y) * 0.5,
		Z: (b.Min.Z + b.Max.Z) * 0.5,
	}
}

func (b AABB) Extent() Vec3 {
	return b.Max.Sub(b.Min)
}

func (b AABB) Union(c AABB) AABB {
	if b.IsEmpty() {
		return c
	}
	if c.IsEmpty() {
		return b
	}
	return AABB{
		Min: Vec3{
			X: math.Min(b.Min.X, c.Min.X),
			Y: math.Min(b.Min.Y, c.Min.Y),
			Z: math.Min(b.Min.Z, c.Min.Z),
		},
		Max: Vec3{
			X: math.Max(b.Max.X, c.Max.X),
			Y: math.Max(b.Max.Y, c.Max.Y),
			Z: math.Max(b.Max.Z, c.Max.Z),
		},
	}
}

func (b AABB) IncludePoint(p Vec3) AABB {
	return b.Union(AABB{Min: p, Max: p})
}

// ExpandedOutward moves each finite bound by about one ulp. It is used only
// for BVH bounds, where a slightly loose box is harmless, while a box that is
// one rounding error too tight could incorrectly prune a valid hit.
func (b AABB) ExpandedOutward() AABB {
	if b.IsEmpty() {
		return b
	}
	return AABB{
		Min: Vec3{
			X: math.Nextafter(b.Min.X, math.Inf(-1)),
			Y: math.Nextafter(b.Min.Y, math.Inf(-1)),
			Z: math.Nextafter(b.Min.Z, math.Inf(-1)),
		},
		Max: Vec3{
			X: math.Nextafter(b.Max.X, math.Inf(1)),
			Y: math.Nextafter(b.Max.Y, math.Inf(1)),
			Z: math.Nextafter(b.Max.Z, math.Inf(1)),
		},
	}
}

// IntersectRay intersects a ray with the box and returns the entering and
// leaving ray parameters. invDir must contain component-wise reciprocals of
// the ray direction, with zero direction components represented by zero.
func (b AABB) IntersectRay(origin, invDir Vec3, minT, maxT float64) (tEnter, tExit float64, ok bool) {
	if b.IsEmpty() {
		return 0, 0, false
	}
	tEnter = minT
	tExit = maxT

	for axis := 0; axis < 3; axis++ {
		var o, inv, lo, hi float64
		switch axis {
		case 0:
			o, inv, lo, hi = origin.X, invDir.X, b.Min.X, b.Max.X
		case 1:
			o, inv, lo, hi = origin.Y, invDir.Y, b.Min.Y, b.Max.Y
		default:
			o, inv, lo, hi = origin.Z, invDir.Z, b.Min.Z, b.Max.Z
		}

		if inv == 0 {
			if o < lo || o > hi {
				return 0, 0, false
			}
			continue
		}
		t0 := (lo - o) * inv
		t1 := (hi - o) * inv
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		if math.IsNaN(t0) || math.IsNaN(t1) {
			return 0, 0, false
		}
		if t0 > tEnter {
			tEnter = t0
		}
		if t1 < tExit {
			tExit = t1
		}
		if tEnter > tExit {
			return 0, 0, false
		}
	}
	return tEnter, tExit, true
}
