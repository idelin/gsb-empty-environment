package bvhtrace

import "testing"

// mollerTrumbore is an independent textbook ray/triangle intersection used
// to validate the watertight kernel. It performs no backface culling.
func mollerTrumbore(r *Ray, tri *Triangle) bool {
	e1 := tri.B.Sub(tri.A)
	e2 := tri.C.Sub(tri.A)
	pvec := r.Dir.Cross(e2)
	det := e1.Dot(pvec)
	const eps = 1e-8
	if det > -eps && det < eps {
		return false
	}
	invDet := 1 / det
	tvec := r.Origin.Sub(tri.A)
	u := tvec.Dot(pvec) * invDet
	if u < 0 || u > 1 {
		return false
	}
	qvec := tvec.Cross(e1)
	v := r.Dir.Dot(qvec) * invDet
	if v < 0 || u+v > 1 {
		return false
	}
	t := e2.Dot(qvec) * invDet
	return t >= r.Tmin && t < r.Tmax
}

// TestCrossValidateMoller compares the watertight kernel's hit/miss decision
// against Moller-Trumbore across many random rays and triangles. The two
// methods can differ only on exact-edge rays (which MT rounds arbitrarily),
// so the interior agreement must be exact.
func TestCrossValidateMoller(t *testing.T) {
	tris := RandomScene(1500, 10, 2024)
	rays := RandomRays(500, 30, 555)

	disagree := 0
	for i := range rays {
		r := rays[i]
		for j := range tris {
			want := mollerTrumbore(&r, &tris[j])
			_, _, _, _, got := intersectTriangle(&r, &tris[j])
			if want != got {
				disagree++
			}
		}
	}
	if disagree != 0 {
		t.Fatalf("watertight kernel disagrees with Moller-Trumbore on %d tests", disagree)
	}
}

func TestTwoSidedAndDistance(t *testing.T) {
	tri := Triangle{A: Vec3{0, 0, 0}, B: Vec3{1, 0, 0}, C: Vec3{0, 1, 0}}

	front := NewRay(Vec3{0.2, 0.3, 2}, Vec3{0, 0, -1}, 0, 0)
	back := NewRay(Vec3{0.2, 0.3, -2}, Vec3{0, 0, 1}, 0, 0)
	for name, ray := range map[string]Ray{"front": front, "back": back} {
		tt, wa, wb, wc, hit := intersectTriangle(&ray, &tri)
		if !hit {
			t.Fatalf("%s: expected hit", name)
		}
		if !approxEqual(tt, 2, 1e-5) {
			t.Fatalf("%s: t=%v want 2", name, tt)
		}
		if !approxEqual(wa, 0.5, 1e-5) || !approxEqual(wb, 0.2, 1e-5) || !approxEqual(wc, 0.3, 1e-5) {
			t.Fatalf("%s: barycentric %v %v %v, want 0.5 0.2 0.3", name, wa, wb, wc)
		}
	}
}
