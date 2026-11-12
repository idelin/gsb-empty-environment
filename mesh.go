package taskpkg

import "fmt"

// Triangle is a triangle in mesh-local space. Its vertices are counter-
// clockwise when viewed from the front side.
type Triangle struct {
	A, B, C Vec3
}

func (t Triangle) Bounds() AABB {
	return EmptyAABB().
		IncludePoint(t.A).
		IncludePoint(t.B).
		IncludePoint(t.C)
}

// FaceNormal returns the unnormalised local face normal cross(B-A, C-A).
func (t Triangle) FaceNormal() Vec3 {
	return t.B.Sub(t.A).Cross(t.C.Sub(t.A))
}

func (t Triangle) IsFinite() bool {
	return t.A.IsFinite() && t.B.IsFinite() && t.C.IsFinite()
}

// Mesh is an immutable triangle mesh with a shared local-space BVH. Multiple
// instances can reference the same Mesh without duplicating or rebuilding it.
type Mesh struct {
	triangles []Triangle
	bounds    AABB
	tree      bvh
}

// NewMesh builds a mesh from vertices and triangle indices. Indices must
// contain three entries per triangle. If indices is empty, vertices are
// interpreted as a triangle soup and len(vertices) must be a multiple of three.
func NewMesh(vertices []Vec3, indices []uint32) (*Mesh, error) {
	for i, v := range vertices {
		if !v.IsFinite() {
			return nil, fmt.Errorf("mesh vertex %d is not finite", i)
		}
	}

	var triangles []Triangle
	switch {
	case len(indices) == 0 && len(vertices) != 0:
		if len(vertices)%3 != 0 {
			return nil, fmt.Errorf("triangle soup has %d vertices, want a multiple of 3", len(vertices))
		}
		triangles = make([]Triangle, len(vertices)/3)
		for i := range triangles {
			triangles[i] = Triangle{
				A: vertices[3*i],
				B: vertices[3*i+1],
				C: vertices[3*i+2],
			}
		}
	case len(indices)%3 != 0:
		return nil, fmt.Errorf("index count %d is not a multiple of 3", len(indices))
	default:
		triangles = make([]Triangle, len(indices)/3)
		for i := range triangles {
			ia, ib, ic := indices[3*i], indices[3*i+1], indices[3*i+2]
			if int(ia) >= len(vertices) || int(ib) >= len(vertices) || int(ic) >= len(vertices) {
				return nil, fmt.Errorf("triangle %d references a vertex outside [0,%d)", i, len(vertices))
			}
			triangles[i] = Triangle{
				A: vertices[ia],
				B: vertices[ib],
				C: vertices[ic],
			}
		}
	}
	return newMeshFromTriangles(triangles), nil
}

func NewTriangleMesh(triangles []Triangle) (*Mesh, error) {
	for i, triangle := range triangles {
		if !triangle.IsFinite() {
			return nil, fmt.Errorf("triangle %d is not finite", i)
		}
	}
	return newMeshFromTriangles(triangles), nil
}

func newMeshFromTriangles(triangles []Triangle) *Mesh {
	m := &Mesh{
		triangles: append([]Triangle(nil), triangles...),
		bounds:    EmptyAABB(),
	}
	bounds := make([]AABB, len(m.triangles))
	for i, triangle := range m.triangles {
		b := triangle.Bounds()
		bounds[i] = b
		m.bounds = m.bounds.Union(b)
	}
	if len(bounds) != 0 {
		m.tree = buildBVH(bounds, bvhLeafSize)
	}
	return m
}

func (m *Mesh) TriangleCount() int {
	if m == nil {
		return 0
	}
	return len(m.triangles)
}

func (m *Mesh) TriangleAt(index int) (Triangle, bool) {
	if m == nil || index < 0 || index >= len(m.triangles) {
		return Triangle{}, false
	}
	return m.triangles[index], true
}

func (m *Mesh) Bounds() AABB {
	if m == nil {
		return EmptyAABB()
	}
	return m.bounds
}

// BVHNodeCount reports the number of nodes in the shared local mesh BVH. It is
// useful for host diagnostics and for confirming that transform updates do not
// rebuild mesh data.
func (m *Mesh) BVHNodeCount() int {
	if m == nil {
		return 0
	}
	return len(m.tree.nodes)
}

func transformAABB(b AABB, transform Affine) AABB {
	if b.IsEmpty() {
		return b
	}
	corners := [8]Vec3{
		{b.Min.X, b.Min.Y, b.Min.Z},
		{b.Max.X, b.Min.Y, b.Min.Z},
		{b.Min.X, b.Max.Y, b.Min.Z},
		{b.Max.X, b.Max.Y, b.Min.Z},
		{b.Min.X, b.Min.Y, b.Max.Z},
		{b.Max.X, b.Min.Y, b.Max.Z},
		{b.Min.X, b.Max.Y, b.Max.Z},
		{b.Max.X, b.Max.Y, b.Max.Z},
	}
	world := EmptyAABB()
	for _, corner := range corners {
		world = world.IncludePoint(transform.TransformPoint(corner))
	}
	return world.ExpandedOutward()
}

func invDir(d Vec3) Vec3 {
	return Vec3{
		X: reciprocalOrZero(d.X),
		Y: reciprocalOrZero(d.Y),
		Z: reciprocalOrZero(d.Z),
	}
}

func reciprocalOrZero(x float64) float64 {
	if x == 0 {
		return 0
	}
	return 1 / x
}
