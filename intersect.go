package taskpkg

const barycentricEpsilon = 1e-10

// intersectTriangleLocal intersects a ray expressed in mesh-local coordinates.
// It returns the local ray parameter and barycentric coordinates. The local
// direction is deliberately not normalised: with d_local = M^-1*d_world and
// o_local = M^-1*(o_world - translation), the local parameter equals the world
// parameter. The scene code still reconstructs the world point and converts
// the parameter back to world before ranking candidates.
func intersectTriangleLocal(tri Triangle, ray Ray) (t, u, v float64, front bool, ok bool) {
	edge1 := tri.B.Sub(tri.A)
	edge2 := tri.C.Sub(tri.A)
	pvec := ray.Direction.Cross(edge2)
	det := edge1.Dot(pvec)
	if det == 0 || !isFinite(det) {
		return 0, 0, 0, false, false
	}

	invDet := 1 / det
	offset := ray.Origin.Sub(tri.A)
	u = offset.Dot(pvec) * invDet
	if u < -barycentricEpsilon || u > 1+barycentricEpsilon {
		return 0, 0, 0, false, false
	}

	qvec := offset.Cross(edge1)
	v = ray.Direction.Dot(qvec) * invDet
	if v < -barycentricEpsilon || u+v > 1+barycentricEpsilon {
		return 0, 0, 0, false, false
	}

	t = edge2.Dot(qvec) * invDet
	if !isFinite(t) {
		return 0, 0, 0, false, false
	}

	// det>0 is exactly dot(local ray direction, local CCW face normal)<0.
	// This logical front side is preserved under det<0 instance transforms,
	// matching the flipped front-face winding rule used by renderers for
	// mirrored meshes.
	front = det > 0
	return t, u, v, front, true
}
