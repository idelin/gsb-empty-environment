package bvhtrace

import "math"

// Triangle is a single geometric primitive. Triangles sharing vertices in the
// mesh must use identical Vec3 values so that shared-edge tests produce
// bit-identical sheared coordinates.
type Triangle struct {
	A, B, C Vec3
}

func absFloat32(x float32) float32 {
	return math.Float32frombits(math.Float32bits(x) &^ (1 << 31))
}

// refSide is an antisymmetric predicate for the sheared 2D direction e:
// refSide(-e) always equals !refSide(e), including exact-zero components.
// It is the sign of cross(e, (1,0)) with deterministic tie-breaking.
func refSide(ex, ey float32) bool {
	if ey < 0 {
		return true
	}
	if ey > 0 {
		return false
	}
	return ex > 0
}

// intersectTriangle implements the watertight ray/triangle intersection of
// Woop, Benthin and Wald, "Watertight Ray/Triangle Intersection" (2013).
//
// The ray is transformed once with a single precision shear, so every
// triangle sharing an edge evaluates that edge with bit-identical
// arithmetic. Exact-zero edge tests are assigned to exactly one of the two
// adjacent triangles via an antisymmetric edge-direction predicate, which
// makes a ray hitting a shared edge neither fall through the mesh nor hit
// both triangles. Front- and back-facing triangles are both reportable.
//
// On a hit it returns the ray distance t and barycentric weights wa, wb, wc
// (summing to 1) for vertices A, B, C respectively. The weight of A is the
// edge function of the opposite edge B->C (U), and so on.
func intersectTriangle(r *Ray, tri *Triangle) (t, wa, wb, wc float32, hit bool) {
	// 1. Choose the axis of the dominant ray direction and permute the
	// coordinate system so that axis becomes the 2D "z" axis.
	ax := absFloat32(r.Dir.X)
	ay := absFloat32(r.Dir.Y)
	az := absFloat32(r.Dir.Z)
	var kz int
	switch {
	case ax > ay && ax > az:
		kz = 0
	case ay > az:
		kz = 1
	default:
		kz = 2
	}
	kx := kz + 1
	if kx == 3 {
		kx = 0
	}
	ky := kx + 1
	if ky == 3 {
		ky = 0
	}
	if r.dirNeg[kz] {
		kx, ky = ky, kx
	}

	// 2. Ray shear constants; the dominant direction component is non-zero.
	dz := r.Dir.component(kz)
	sx := r.Dir.component(kx) / dz
	sy := r.Dir.component(ky) / dz
	sz := 1 / dz

	// Sheared ray origin.
	oz := r.Origin.component(kz)
	ox := r.Origin.component(kx) - sx*oz
	oy := r.Origin.component(ky) - sy*oz

	// 3. Sheared triangle vertices relative to the ray origin.
	axv := tri.A.component(kx) - sx*tri.A.component(kz) - ox
	ayv := tri.A.component(ky) - sy*tri.A.component(kz) - oy
	bxv := tri.B.component(kx) - sx*tri.B.component(kz) - ox
	byv := tri.B.component(ky) - sy*tri.B.component(kz) - oy
	cxv := tri.C.component(kx) - sx*tri.C.component(kz) - ox
	cyv := tri.C.component(ky) - sy*tri.C.component(kz) - oy

	// 4. Scaled edge functions following Woop et al. 2013. In sheared 2D
	// space the ray origin is (0,0); U, V, W are the signed edge functions
	// of edges B-C, C-A and A-B and U+V+W is the scaled signed area det.
	u := cxv*byv - cyv*bxv
	v := axv*cyv - ayv*cxv
	w := bxv*ayv - byv*axv
	det := u + v + w
	if det == 0 {
		return 0, 0, 0, 0, false
	}

	// 5. Edge tests. Normalize the winding so the hit test always uses one
	// non-negative sign convention: for a back-facing triangle (det < 0)
	// negate every edge function and the determinant. Barycentric weights
	// and t are ratios, so this common sign flip leaves them unchanged.
	//
	// An exact zero is assigned to exactly one side of a shared edge by an
	// antisymmetric predicate on the edge direction, so the two triangles
	// adjacent to the edge make complementary decisions: the ray neither
	// leaks through the shared edge nor hits both triangles.
	if det < 0 {
		u, v, w, det = -u, -v, -w, -det
	}

	// refSide receives the geometric edge direction; after negation a
	// back-facing triangle keeps the same complementary zero assignment.
	passU := u > 0 || (u == 0 && refSide(cxv-bxv, cyv-byv))
	passV := v > 0 || (v == 0 && refSide(axv-cxv, ayv-cyv))
	passW := w > 0 || (w == 0 && refSide(bxv-axv, byv-ayv))
	if !passU || !passV || !passW {
		return 0, 0, 0, 0, false
	}

	// 6. Distance along the ray from the sheared z coordinate of the hit.
	azShear := tri.A.component(kz)
	bzShear := tri.B.component(kz)
	czShear := tri.C.component(kz)
	// U is the barycentric weight of A, V of B and W of C.
	hitZ := (u*azShear + v*bzShear + w*czShear) / det
	// The sheared ray is (0,0,oz)+t*(0,0,1), giving t = Sz*(hitZ - oz).
	t = (hitZ - oz) * sz
	if t < r.Tmin || t >= r.Tmax {
		return 0, 0, 0, 0, false
	}

	invDet := 1 / det
	// Edge U runs B->C so it is the weight of A, V (C->A) the weight of B,
	// and W (A->B) the weight of C.
	return t, u * invDet, v * invDet, w * invDet, true
}
