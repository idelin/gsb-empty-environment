package meshsimp

import (
	"errors"
	"math"
)

// Mesh is an indexed triangle mesh with optional per-vertex attributes.
// Attribute slices, when non-nil, must have the same length as Positions and
// are interpolated during collapses using the surviving/removed vertex
// positions as interpolation weights.
type Mesh struct {
	Positions [][3]float64
	Normals   [][3]float64
	UVs       [][2]float64
	Colors    [][3]float64

	// Faces index into Positions. Every face has exactly three vertices.
	Faces [][3]int

	// NormalizeAttributes re-normalizes interpolated normals and colors after
	// each collapse.
	NormalizeAttributes bool
}

var (
	errNoFaces        = errors.New("meshsimp: mesh has no faces")
	errBadFaceIndex   = errors.New("meshsimp: face references a vertex outside Positions")
	errDegenerateFace = errors.New("meshsimp: mesh contains a zero-area face")
)

// clone returns a deep copy so simplification never mutates caller data.
func (m *Mesh) clone() *Mesh {
	cp := func(slice [][3]float64) [][3]float64 {
		if slice == nil {
			return nil
		}
		out := make([][3]float64, len(slice))
		copy(out, slice)
		return out
	}
	cp2 := func(slice [][2]float64) [][2]float64 {
		if slice == nil {
			return nil
		}
		out := make([][2]float64, len(slice))
		copy(out, slice)
		return out
	}
	return &Mesh{
		Positions:           cp(m.Positions),
		Normals:             cp(m.Normals),
		UVs:                 cp2(m.UVs),
		Colors:              cp(m.Colors),
		Faces:               append([][3]int(nil), m.Faces...),
		NormalizeAttributes: m.NormalizeAttributes,
	}
}

// Validate checks basic mesh invariants.
func (m *Mesh) Validate() error {
	if len(m.Faces) == 0 {
		return errNoFaces
	}
	n := len(m.Positions)
	for _, f := range m.Faces {
		for _, idx := range f {
			if idx < 0 || idx >= n {
				return errBadFaceIndex
			}
		}
		if f[0] == f[1] || f[1] == f[2] || f[2] == f[0] {
			return errDegenerateFace
		}
		if area(m.Positions[f[0]], m.Positions[f[1]], m.Positions[f[2]]) == 0 {
			return errDegenerateFace
		}
	}
	for _, attr := range [][][3]float64{m.Normals, m.Colors} {
		if attr != nil && len(attr) != n {
			return errors.New("meshsimp: vector attribute length must match Positions")
		}
	}
	if m.UVs != nil && len(m.UVs) != n {
		return errors.New("meshsimp: UV length must match Positions")
	}
	return nil
}

// IsManifold reports whether every edge is shared by at most two faces and
// two-face edges have consistent opposite winding. A mesh without boundary
// is a closed manifold when this returns true.
func (m *Mesh) IsManifold() bool {
	type edgeUse struct {
		count  int
		orient bool
	}
	edges := make(map[uint64]edgeUse, len(m.Faces)*3/2)
	for _, f := range m.Faces {
		for k := 0; k < 3; k++ {
			a, b := f[k], f[(k+1)%3]
			key := edgeKey(a, b)
			use := edges[key]
			use.count++
			if use.count > 2 {
				return false
			}
			if a < b {
				use.orient = !use.orient
			}
			edges[key] = use
		}
	}
	for _, use := range edges {
		// With consistent winding, one face traverses the canonical edge
		// forward (a<b toggles orient) and the other backward, so orient
		// ends true. Staying false means duplicated/inconsistent winding.
		if use.count == 2 && !use.orient {
			return false
		}
	}
	return true
}

// BoundaryEdges returns the set of undirected edges used by exactly one face.
// Edge keys are produced with EdgeKey.
func (m *Mesh) BoundaryEdges() map[uint64]struct{} {
	type edgeUse struct {
		count int
	}
	edges := make(map[uint64]edgeUse, len(m.Faces)*3/2)
	for _, f := range m.Faces {
		for k := 0; k < 3; k++ {
			use := edges[edgeKey(f[k], f[(k+1)%3])]
			use.count++
			edges[edgeKey(f[k], f[(k+1)%3])] = use
		}
	}
	boundary := make(map[uint64]struct{})
	for key, use := range edges {
		if use.count == 1 {
			boundary[key] = struct{}{}
		}
	}
	return boundary
}

// EdgeKey returns the canonical undirected key for the edge between a and b.
func EdgeKey(a, b int) uint64 { return edgeKey(a, b) }

func edgeKey(a, b int) uint64 {
	if a > b {
		a, b = b, a
	}
	return uint64(uint32(a))<<32 | uint64(uint32(b))
}

func keyEnds(key uint64) (int, int) {
	return int(key >> 32), int(key & 0xffffffff)
}

func sub(a, b [3]float64) [3]float64 {
	return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]}
}

func cross(a, b [3]float64) [3]float64 {
	return [3]float64{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

func dot(a, b [3]float64) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

func norm(a [3]float64) float64 { return math.Sqrt(dot(a, a)) }

func normalize(a [3]float64) [3]float64 {
	l := norm(a)
	if l == 0 {
		return a
	}
	return [3]float64{a[0] / l, a[1] / l, a[2] / l}
}

func area(a, b, c [3]float64) float64 {
	return norm(cross(sub(b, a), sub(c, a))) * 0.5
}

// faceNormal returns the unit normal of face f in the given positions.
func faceNormal(positions [][3]float64, f [3]int) [3]float64 {
	return normalize(cross(
		sub(positions[f[1]], positions[f[0]]),
		sub(positions[f[2]], positions[f[0]]),
	))
}
