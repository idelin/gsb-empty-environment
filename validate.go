package taskpkg

import "fmt"

// Mesh is the merged, stitched triangle mesh.
type Mesh struct {
	Points    map[PointID]Vec3
	Triangles []Triangle
}

// Merge combines per-patch meshes into one mesh. Points shared between
// patches must have bitwise identical positions; a mismatch is reported
// as an error, which indicates a stitching defect (crack).
func Merge(pms []PatchMesh) (*Mesh, error) {
	m := &Mesh{Points: make(map[PointID]Vec3)}
	for _, pm := range pms {
		for id, pos := range pm.Points {
			if prev, ok := m.Points[id]; ok {
				if prev != pos {
					return nil, fmt.Errorf("taskpkg: shared point %+v has mismatched positions %v and %v", id, prev, pos)
				}
			} else {
				m.Points[id] = pos
			}
		}
		m.Triangles = append(m.Triangles, pm.Triangles...)
	}
	return m, nil
}

func lessPointID(a, b PointID) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	switch a.Kind {
	case CornerPoint:
		return a.Vert < b.Vert
	case EdgePoint:
		if a.Edge != b.Edge {
			if a.Edge.Lo != b.Edge.Lo {
				return a.Edge.Lo < b.Edge.Lo
			}
			return a.Edge.Hi < b.Edge.Hi
		}
		if a.Den != b.Den {
			return a.Den < b.Den
		}
		return a.Num < b.Num
	default:
		if a.Patch != b.Patch {
			return a.Patch < b.Patch
		}
		if a.I != b.I {
			return a.I < b.I
		}
		return a.J < b.J
	}
}

// Validate checks that the stitched mesh is an oriented manifold triangle
// mesh: every edge is shared by exactly two triangles with opposite
// traversal directions (consistent normals), or, when closed is false, may
// also belong to a single boundary triangle. Degenerate triangles are
// rejected.
func (m *Mesh) Validate(closed bool) error {
	type pair struct{ a, b PointID }
	directed := map[pair]int{}
	undirected := map[pair]int{}
	canon := func(a, b PointID) pair {
		if lessPointID(b, a) {
			return pair{b, a}
		}
		return pair{a, b}
	}
	for _, t := range m.Triangles {
		if t[0] == t[1] || t[1] == t[2] || t[2] == t[0] {
			return fmt.Errorf("taskpkg: degenerate triangle %v", t)
		}
		for e := 0; e < 3; e++ {
			a, b := t[e], t[(e+1)%3]
			directed[pair{a, b}]++
			undirected[canon(a, b)]++
		}
	}
	for e, n := range undirected {
		switch {
		case n == 1:
			if closed {
				return fmt.Errorf("taskpkg: boundary edge %v in a supposedly closed mesh", e)
			}
		case n == 2:
			if directed[pair{e.a, e.b}] != 1 || directed[pair{e.b, e.a}] != 1 {
				return fmt.Errorf("taskpkg: inconsistent triangle orientation across edge %v", e)
			}
		default:
			return fmt.Errorf("taskpkg: edge %v shared by %d triangles", e, n)
		}
	}
	return nil
}

// Stats reports triangle, internal-edge and boundary-edge counts. Every
// internal edge borders exactly two triangles.
func (m *Mesh) Stats() (triangles, internalEdges, boundaryEdges int) {
	type pair struct{ a, b PointID }
	undirected := map[pair]int{}
	canon := func(a, b PointID) pair {
		if lessPointID(b, a) {
			return pair{b, a}
		}
		return pair{a, b}
	}
	for _, t := range m.Triangles {
		for e := 0; e < 3; e++ {
			undirected[canon(t[e], t[(e+1)%3])]++
		}
	}
	triangles = len(m.Triangles)
	for _, n := range undirected {
		if n == 2 {
			internalEdges++
		} else {
			boundaryEdges++
		}
	}
	return triangles, internalEdges, boundaryEdges
}

// Normal returns the (unnormalized) geometric normal of a triangle from
// its winding, using the merged positions.
func (m *Mesh) Normal(t Triangle) Vec3 {
	a := m.Points[t[0]]
	b := m.Points[t[1]]
	c := m.Points[t[2]]
	return b.Sub(a).Cross(c.Sub(a))
}

// Centroid returns the geometric center of a triangle.
func (m *Mesh) Centroid(t Triangle) Vec3 {
	a := m.Points[t[0]]
	b := m.Points[t[1]]
	c := m.Points[t[2]]
	return a.Add(b).Add(c).Scale(1.0 / 3.0)
}
