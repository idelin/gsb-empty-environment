package bvhtrace

// AABB is an axis-aligned bounding box.
type AABB struct {
	Min Vec3
	Max Vec3
}

func emptyAABB() AABB {
	inf := infinity32
	return AABB{
		Min: Vec3{inf, inf, inf},
		Max: Vec3{-inf, -inf, -inf},
	}
}

func (b AABB) extend(o AABB) AABB {
	return AABB{
		Min: Vec3{min(b.Min.X, o.Min.X), min(b.Min.Y, o.Min.Y), min(b.Min.Z, o.Min.Z)},
		Max: Vec3{max(b.Max.X, o.Max.X), max(b.Max.Y, o.Max.Y), max(b.Max.Z, o.Max.Z)},
	}
}

func (b AABB) center() Vec3 {
	return Vec3{
		(b.Min.X + b.Max.X) * 0.5,
		(b.Min.Y + b.Max.Y) * 0.5,
		(b.Min.Z + b.Max.Z) * 0.5,
	}
}

func (b AABB) surfaceArea() float32 {
	if b.Min.X > b.Max.X {
		return 0
	}
	dx := b.Max.X - b.Min.X
	dy := b.Max.Y - b.Min.Y
	dz := b.Max.Z - b.Min.Z
	return 2 * (dx*dy + dy*dz + dz*dx)
}

// intersect ray-intersects the box, returning the clipped entry/exit
// distances. Rays parallel to a slab are handled explicitly so the division
// never produces NaNs; the box then only needs to contain the ray origin on
// that axis.
func (b AABB) intersect(r *Ray) (tNear, tFar float32, hit bool) {
	tNear = r.Tmin
	tFar = r.Tmax
	for axis := 0; axis < 3; axis++ {
		lo := b.Min.component(axis)
		hi := b.Max.component(axis)
		origin := r.Origin.component(axis)
		inv := r.invDir.component(axis)
		// A zero direction yields an infinite inverse; multiplying an
		// infinite value by 0 produces NaN, which propagates through the
		// min/max and forces the slab comparison below to fail correctly.
		t1 := (lo - origin) * inv
		t2 := (hi - origin) * inv
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tNear {
			tNear = t1
		}
		if t2 < tFar {
			tFar = t2
		}
		if tNear > tFar {
			return 0, 0, false
		}
	}
	return tNear, tFar, true
}
