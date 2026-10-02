package hexgrid

import "math"

// res0Gnomonic is the gnomonic radius of a resolution-0 hexagon. It matches
// the inradius of the icosahedron face triangle under gnomonic projection,
// so that resolution 0 cells roughly cover their faces.
const res0Gnomonic = 0.38196601125010500003

// maxRes is the finest supported resolution. i/j grid coordinates stay well
// within the 27-bit fields of the Cell encoding at this resolution.
const maxRes = 15

var sqrt7 = math.Sqrt(7)

// faceGeom holds the precomputed geometry of one icosahedron face: its
// outward unit normal n (also the face center direction) and an orthonormal
// in-plane basis (u, v) used for the gnomonic projection.
type faceGeom struct {
	n, u, v vec3
}

var faces [20]faceGeom

// scaleByRes[r] converts hex-grid units at resolution r to gnomonic units.
var scaleByRes [maxRes + 1]float64

// safeRadius[r] is the largest ijk hex-distance from a face center for which
// a cell is guaranteed to be owned by its face (its center lies inside the
// face's Voronoi region). K-ring traversals whose whole disk stays within
// this radius take a pure-integer fast path.
var safeRadius [maxRes + 1]int

func init() {
	buildIcosahedron()
	for r := 0; r <= maxRes; r++ {
		scaleByRes[r] = res0Gnomonic / math.Pow(sqrt7, float64(r))
	}
	// Ownership is Voronoi-based: a cell belongs to the face whose center
	// is nearest to the cell center. The safe radius is the minimum
	// distance from a face center to any Voronoi boundary, in grid units.
	inradius := math.MaxFloat64
	for f := 0; f < 20; f++ {
		for g := 0; g < 20; g++ {
			if g == f {
				continue
			}
			if d := voronoiBoundaryDist(faces[f], faces[g]); d < inradius {
				inradius = d
			}
		}
	}
	for r := 0; r <= maxRes; r++ {
		safeRadius[r] = int(inradius/scaleByRes[r]) - 2
	}
}

// voronoiBoundaryDist returns the 2D distance from a face center to the
// Voronoi bisector line with another face, measured in the gnomonic plane.
func voronoiBoundaryDist(f, g faceGeom) float64 {
	d := f.n.sub(g.n)
	c0 := f.n.dot(d) // == 1 - f.n.g.n > 0
	a := f.u.dot(d)
	b := f.v.dot(d)
	return c0 / math.Hypot(a, b)
}

// buildIcosahedron constructs the 12 vertices and 20 faces of a regular
// icosahedron and precomputes per-face projection bases.
func buildIcosahedron() {
	phi := (1 + math.Sqrt(5)) / 2
	raw := []vec3{
		{0, 1, phi}, {0, 1, -phi}, {0, -1, phi}, {0, -1, -phi},
		{1, phi, 0}, {1, -phi, 0}, {-1, phi, 0}, {-1, -phi, 0},
		{phi, 0, 1}, {-phi, 0, 1}, {phi, 0, -1}, {-phi, 0, -1},
	}
	verts := make([]vec3, len(raw))
	for i, v := range raw {
		verts[i] = v.unit()
	}
	// Edge length is the minimum pairwise vertex distance.
	edge := math.MaxFloat64
	for i := 0; i < len(verts); i++ {
		for j := i + 1; j < len(verts); j++ {
			if d := verts[i].sub(verts[j]).norm(); d < edge {
				edge = d
			}
		}
	}
	// A face is any triple of mutually edge-distant vertices.
	nf := 0
	for a := 0; a < len(verts) && nf < 20; a++ {
		for b := a + 1; b < len(verts) && nf < 20; b++ {
			for c := b + 1; c < len(verts) && nf < 20; c++ {
				if !isEdge(verts[a], verts[b], edge) ||
					!isEdge(verts[b], verts[c], edge) ||
					!isEdge(verts[a], verts[c], edge) {
					continue
				}
				va, vb, vc := verts[a], verts[b], verts[c]
				center := va.add(vb).add(vc)
				if vb.sub(va).cross(vc.sub(va)).dot(center) < 0 {
					vb, vc = vc, vb // keep outward winding
				}
				var g faceGeom
				g.n = center.unit()
				g.u = va.sub(g.n.scale(va.dot(g.n))).unit()
				g.v = g.n.cross(g.u)
				faces[nf] = g
				nf++
			}
		}
	}
	if nf != 20 {
		panic("icosahedron construction failed")
	}
}

func isEdge(a, b vec3, edge float64) bool {
	d := a.sub(b).norm()
	return math.Abs(d-edge) < 1e-9
}

// faceCenter returns the unit direction of a face center.
func faceCenter(f int) vec3 { return faces[f].n }

// bestFace returns the index of the face whose center is closest to the
// (unnormalized) direction dir. Pure dot products, no trig.
//
// Cell centers that sit exactly on a Voronoi boundary (e.g. resolution-0
// cells centered on icosahedron edge midpoints, which two adjacent face
// grids share physically) tie within floating-point noise. Ties are broken
// deterministically in favor of the lowest face index, so every code path
// assigns the same canonical face regardless of rounding.
func bestFace(dir vec3) int {
	best, bestDot := 0, -math.MaxFloat64
	for f := 0; f < 20; f++ {
		if d := dir.dot(faces[f].n); d > bestDot*(1+1e-9) {
			best, bestDot = f, d
		}
	}
	return best
}
