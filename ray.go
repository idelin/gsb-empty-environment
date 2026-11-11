package bvhtrace

import "math"

var infinity32 = float32(math.Inf(1))

// Ray is a single shading ray. Tmax is the current closest-hit distance;
// any intersection with t >= Tmax is ignored. Shadow rays can stop as soon
// as a hit with t < Tmax is found.
type Ray struct {
	Origin Vec3
	Dir    Vec3
	Tmin   float32
	Tmax   float32
	invDir Vec3
	dirNeg [3]bool
}

// NewRay constructs a ray and caches data used by BVH traversal. tmax <= 0
// means an effectively infinite ray interval.
func NewRay(origin, dir Vec3, tmin, tmax float32) Ray {
	r := Ray{Origin: origin, Dir: dir, Tmin: tmin, Tmax: tmax}
	if tmax <= 0 {
		r.Tmax = math.MaxFloat32
	}
	for axis := 0; axis < 3; axis++ {
		d := dir.component(axis)
		if d == 0 {
			r.invDir.setComponent(axis, infinity32)
		} else {
			r.invDir.setComponent(axis, 1/d)
			if d < 0 {
				r.dirNeg[axis] = true
			}
		}
	}
	return r
}

func (v *Vec3) setComponent(axis int, value float32) {
	switch axis {
	case 0:
		v.X = value
	case 1:
		v.Y = value
	default:
		v.Z = value
	}
}
