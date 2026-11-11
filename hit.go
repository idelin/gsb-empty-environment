package bvhtrace

// Hit describes the closest intersection found along a ray.
type Hit struct {
	// T is the parametric distance from the ray origin.
	T float32
	// Primitive is the index of the hit triangle inside the BVH mesh.
	Primitive int
	// WA, WB, WC are barycentric weights for triangle vertices A, B, C and
	// sum to 1; use them to interpolate per-vertex shading data.
	WA, WB, WC float32
}

// Point reconstructs the intersection position in world space.
func (h Hit) Point(tri *Triangle) Vec3 {
	return tri.A.Scale(h.WA).Add(tri.B.Scale(h.WB)).Add(tri.C.Scale(h.WC))
}
