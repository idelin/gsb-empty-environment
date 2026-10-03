package hexgrid

import (
	"math"
	"sort"
)

// vec3 is a 3D cartesian vector on the unit sphere or a tangent plane.
type vec3 struct {
	X, Y, Z float64
}

func (a vec3) dot(b vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a vec3) add(b vec3) vec3      { return vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a vec3) sub(b vec3) vec3      { return vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a vec3) scale(s float64) vec3 { return vec3{a.X * s, a.Y * s, a.Z * s} }
func (a vec3) cross(b vec3) vec3 {
	return vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}
func (a vec3) norm() vec3 { return a.scale(1 / math.Sqrt(a.dot(a))) }

// face describes one icosahedron base cell: its spherical triangle,
// tangent-plane 2D basis, and adjacent faces.
type face struct {
	ID     int
	Verts  [3]int // indices into icosahedronVertices
	Center vec3   // unit outward normal through the face center
	U, V   vec3   // orthonormal 2D basis of the tangent plane
	Adj    [3]int // adjacent face ids (share an edge)
}

var faces, faceInradius = buildFaces()

// icosahedronVertices returns the 12 unit-sphere icosahedron vertices.
func icosahedronVertices() []vec3 {
	t := (1 + math.Sqrt(5)) / 2
	raw := []vec3{
		{-1, t, 0}, {1, t, 0}, {-1, -t, 0}, {1, -t, 0},
		{0, -1, t}, {0, 1, t}, {0, -1, -t}, {0, 1, -t},
		{t, 0, -1}, {t, 0, 1}, {-t, 0, -1}, {-t, 0, 1},
	}
	out := make([]vec3, len(raw))
	for i, v := range raw {
		out[i] = v.norm()
	}
	return out
}

// buildFaces enumerates the 20 icosahedron faces deterministically
// (sorted by face-center latitude then longitude), computes per-face
// tangent bases and adjacency, and returns the faces plus the inradius
// of the gnomonically projected face triangle.
func buildFaces() ([]face, float64) {
	verts := icosahedronVertices()

	edge2 := math.Inf(1)
	for i := 0; i < len(verts); i++ {
		for j := i + 1; j < len(verts); j++ {
			if d := verts[i].sub(verts[j]).dot(verts[i].sub(verts[j])); d < edge2 {
				edge2 = d
			}
		}
	}
	isEdge := func(a, b int) bool {
		d := verts[a].sub(verts[b]).dot(verts[a].sub(verts[b]))
		return math.Abs(d-edge2) < 1e-9
	}

	var fs []face
	for a := 0; a < len(verts); a++ {
		for b := a + 1; b < len(verts); b++ {
			if !isEdge(a, b) {
				continue
			}
			for c := b + 1; c < len(verts); c++ {
				if !isEdge(a, c) || !isEdge(b, c) {
					continue
				}
				ctr := verts[a].add(verts[b]).add(verts[c]).norm()
				fs = append(fs, face{Verts: [3]int{a, b, c}, Center: ctr})
			}
		}
	}
	sort.Slice(fs, func(x, y int) bool {
		latX, lngX := vecToLatLngRad(fs[x].Center)
		latY, lngY := vecToLatLngRad(fs[y].Center)
		if latX != latY {
			return latX < latY
		}
		return lngX < lngY
	})
	for i := range fs {
		fs[i].ID = i
	}

	for i := range fs {
		var adj []int
		for j := range fs {
			if i == j {
				continue
			}
			shared := 0
			for _, vi := range fs[i].Verts {
				for _, vj := range fs[j].Verts {
					if vi == vj {
						shared++
					}
				}
			}
			if shared == 2 {
				adj = append(adj, j)
			}
		}
		copy(fs[i].Adj[:], adj)
	}

	for i := range fs {
		n := fs[i].Center
		up := vec3{0, 0, 1}
		if math.Abs(n.Z) > 0.9 {
			up = vec3{1, 0, 0}
		}
		u := n.cross(up).norm()
		fs[i].U = u
		fs[i].V = n.cross(u).norm()
	}

	// Inradius of the gnomonically projected face triangle (identical
	// for all faces; use face 0).
	f := fs[0]
	var pts [3]vec3
	for k, vi := range f.Verts {
		v := verts[vi]
		pts[k] = v.scale(1 / v.dot(f.Center))
	}
	side := func(a, b vec3) float64 {
		d := a.sub(b)
		return math.Sqrt(d.dot(d))
	}
	la, lb, lc := side(pts[0], pts[1]), side(pts[1], pts[2]), side(pts[2], pts[0])
	s := (la + lb + lc) / 2
	inradius := math.Sqrt(s*(s-la)*(s-lb)*(s-lc)) / s

	return fs, inradius
}

// FaceCenter returns the lat/lng (degrees) of a base cell's center.
func FaceCenter(baseCell int) LatLng {
	lat, lng := vecToLatLngRad(faces[baseCell].Center)
	return LatLng{Lat: lat * 180 / math.Pi, Lng: lng * 180 / math.Pi}
}

// FaceAdjacency returns the 3 base cells adjacent to the given one.
func FaceAdjacency(baseCell int) [3]int { return faces[baseCell].Adj }

func vecToLatLngRad(v vec3) (lat, lng float64) {
	return math.Asin(v.Z), math.Atan2(v.Y, v.X)
}
