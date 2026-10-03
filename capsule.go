package stgrid

import "math"

const eps = 1e-12

// Capsule is the swept volume of an entity during one time slot: the
// segment from P0 to P1 (extrapolated from the velocity vector) inflated
// by Radius. Two capsules intersect iff their center lines come within
// Radius1+Radius2 of each other.
type Capsule struct {
	P0, P1 Vec3
	Radius float64
}

// Intersects reports whether two capsules touch or overlap.
func (c Capsule) Intersects(o Capsule) bool {
	return SegmentDistance(c.P0, c.P1, o.P0, o.P1) <= c.Radius+o.Radius
}

// SegmentDistance returns the minimum distance between segments p1q1 and
// p2q2 (Ericson, Real-Time Collision Detection, 5.1.9).
func SegmentDistance(p1, q1, p2, q2 Vec3) float64 {
	d1 := q1.Sub(p1)
	d2 := q2.Sub(p2)
	r := p1.Sub(p2)
	a := d1.Dot(d1)
	e := d2.Dot(d2)
	f := d2.Dot(r)

	var s, t float64
	switch {
	case a <= eps && e <= eps:
		return r.Len()
	case a <= eps:
		t = clamp01(f / e)
	default:
		c := d1.Dot(r)
		if e <= eps {
			s = clamp01(-c / a)
			break
		}
		b := d1.Dot(d2)
		denom := a*e - b*b
		if denom > eps {
			s = clamp01((b*f - c*e) / denom)
		}
		t = (b*s + f) / e
		switch {
		case t < 0:
			t = 0
			s = clamp01(-c / a)
		case t > 1:
			t = 1
			s = clamp01((b - c) / a)
		}
	}
	c1 := p1.Add(d1.Scale(s))
	c2 := p2.Add(d2.Scale(t))
	return c1.Sub(c2).Len()
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// BreachWindow returns the earliest time t in [0, dt] at which two points,
// starting at relative position r and moving at constant relative velocity
// v, come within separation sep. ok is false when no breach happens inside
// the window. Solved analytically from |r + v*t|^2 = sep^2.
func BreachWindow(r, v Vec3, sep, dt float64) (t float64, ok bool) {
	a := v.Dot(v)
	c := r.Dot(r) - sep*sep
	if c <= 0 {
		return 0, true // already inside the safety bubble
	}
	if a < eps {
		return 0, false // constant separation, currently outside
	}
	b := 2 * r.Dot(v)
	if -b/(2*a) <= 0 {
		return 0, false // distance is stationary or increasing
	}
	disc := b*b - 4*a*c
	if disc < 0 {
		return 0, false
	}
	t1 := (-b - math.Sqrt(disc)) / (2 * a)
	if t1 > dt {
		return 0, false
	}
	if t1 < 0 {
		t1 = 0
	}
	return t1, true
}

// MinApproach returns the minimum separation over [0, dt] for two points
// with relative position r and relative velocity v.
func MinApproach(r, v Vec3, dt float64) float64 {
	a := v.Dot(v)
	if a < eps {
		return r.Len()
	}
	t := -r.Dot(v) / a
	if t < 0 {
		t = 0
	}
	if t > dt {
		t = dt
	}
	return r.Add(v.Scale(t)).Len()
}
