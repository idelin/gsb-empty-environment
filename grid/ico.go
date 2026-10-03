package grid

import "math"

// vec3 is a 3D vector on the unit sphere / face planes.
type vec3 struct{ x, y, z float64 }

func (a vec3) add(b vec3) vec3      { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3      { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) scale(s float64) vec3 { return vec3{a.x * s, a.y * s, a.z * s} }
func (a vec3) dot(b vec3) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec3) cross(b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}
func (a vec3) norm() vec3 {
	l := math.Sqrt(a.dot(a))
	return vec3{a.x / l, a.y / l, a.z / l}
}

const numFaces = 20

// faceGeom holds the planar geometry of one icosahedron face. The hexagonal
// lattice lives directly on the face plane with basis vectors aligned to two
// of the face edges (60 deg apart). Because every icosahedron edge has the
// same length and every face uses the same lattice spacing, lattices of
// adjacent faces coincide along shared edges, which makes cross-face
// quantization consistent.
type faceGeom struct {
	origin vec3 // vertex A of the face
	b1, b2 vec3 // unit lattice basis along edges AB and AC
	d1, d2 vec3 // dual basis: d_i . b_j = kronecker delta
	normal vec3 // outward unit normal
	offset float64
}

var (
	icoVerts [12]vec3
	icoFaces [numFaces][3]int
	faces    [numFaces]faceGeom
	edgeLen  float64
)

func init() {
	t := (1 + math.Sqrt(5)) / 2
	raw := [12]vec3{
		{-1, t, 0}, {1, t, 0}, {-1, -t, 0}, {1, -t, 0},
		{0, -1, t}, {0, 1, t}, {0, -1, -t}, {0, 1, -t},
		{t, 0, -1}, {t, 0, 1}, {-t, 0, -1}, {-t, 0, 1},
	}
	for i, v := range raw {
		icoVerts[i] = v.norm()
	}
	icoFaces = [numFaces][3]int{
		{0, 11, 5}, {0, 5, 1}, {0, 1, 7}, {0, 7, 10}, {0, 10, 11},
		{1, 5, 9}, {5, 11, 4}, {11, 10, 2}, {10, 7, 6}, {7, 1, 8},
		{3, 9, 4}, {3, 4, 2}, {3, 2, 6}, {3, 6, 8}, {3, 8, 9},
		{4, 9, 5}, {2, 4, 11}, {6, 2, 10}, {8, 6, 7}, {9, 8, 1},
	}
	a0 := icoVerts[icoFaces[0][0]]
	b0 := icoVerts[icoFaces[0][1]]
	d := b0.sub(a0)
	edgeLen = math.Sqrt(d.dot(d))

	for i, f := range icoFaces {
		A, B, C := icoVerts[f[0]], icoVerts[f[1]], icoVerts[f[2]]
		b1 := B.sub(A).norm()
		b2 := C.sub(A).norm()
		n := b1.cross(b2).norm()
		centroid := A.add(B).add(C).scale(1.0 / 3.0)
		if n.dot(centroid) < 0 {
			// Reorder vertices (swap B and C) so every face frame is
			// right-handed with an outward normal. A uniform frame
			// handedness across all faces keeps the hex lattices of
			// adjacent faces aligned in the unfolded net.
			B, C = C, B
			b1 = B.sub(A).norm()
			b2 = C.sub(A).norm()
			n = b1.cross(b2).norm()
		}
		// Dual basis of {b1, b2} (b1.b2 = cos 60 = 0.5).
		d1 := b1.sub(b2.scale(0.5)).scale(1.0 / 0.75)
		d2 := b2.sub(b1.scale(0.5)).scale(1.0 / 0.75)
		faces[i] = faceGeom{
			origin: A, b1: b1, b2: b2, d1: d1, d2: d2,
			normal: n, offset: A.dot(n),
		}
	}
}
