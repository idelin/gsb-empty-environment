// Package hexgrid implements a discrete hexagonal hierarchical spatial
// index built on an icosahedral unfolding of the sphere, together with
// an offline integrity inspector and a formal invariant asserter.
//
// The sphere is partitioned into the 20 faces of an icosahedron. Each
// face carries a regular hexagonal grid in its own tangent plane
// (gnomonic projection). A cell is identified by (face, i, j) at a
// resolution r, where i,j in [0, 2^r). The hierarchy has aperture 4:
// every cell has exactly 4 children and 1 parent, which makes all
// hierarchy operations exact bit arithmetic.
package hexgrid

import "math"

// MaxRes is the finest supported resolution. The cell index needs
// 2*res bits inside a 51-bit index field, so 15 is the maximum.
const MaxRes = 15

// numFaces is the number of icosahedron faces.
const numFaces = 20

// phi is the golden ratio, used to place icosahedron vertices.
const phi = 1.6180339887498948482

// icoFaces lists the 20 triangular faces as triples of vertex indices
// into the normalized icosahedron vertex set.
var icoFaces = [numFaces][3]int{
	{0, 11, 5}, {0, 5, 1}, {0, 1, 7}, {0, 7, 10}, {0, 10, 11},
	{1, 5, 9}, {5, 11, 4}, {11, 10, 2}, {10, 7, 6}, {7, 1, 8},
	{3, 9, 4}, {3, 4, 2}, {3, 2, 6}, {3, 6, 8}, {3, 8, 9},
	{4, 9, 5}, {2, 4, 11}, {6, 2, 10}, {8, 6, 7}, {9, 8, 1},
}

// faceGeom holds the precomputed, immutable geometry of one
// icosahedron face: its unit center vector, an orthonormal basis
// (xAxis, yAxis) of the tangent plane at the center, and the
// circumradius rho of the gnomonically projected face triangle.
type faceGeom struct {
	center [3]float64
	xAxis  [3]float64
	yAxis  [3]float64
	rho    float64
}

// faces is initialized once in init and never mutated afterwards,
// which makes every package-level function safe for concurrent use.
var faces [numFaces]faceGeom

// edgeLine is a 2D line in a face's tangent plane, stored as
// nx*px + ny*py + c = 0 with the unit normal pointing inwards.
// Signed distance from the face triangle edge is dot + c.
type edgeLine struct {
	nx, ny, c float64
}

// faceEdges holds the 3 inward edge lines of each face's projected
// triangle, used to tell interior cells from seam-adjacent cells.
var faceEdges [numFaces][3]edgeLine

func init() {
	var verts [12][3]float64
	raw := [12][3]float64{
		{-1, phi, 0}, {1, phi, 0}, {-1, -phi, 0}, {1, -phi, 0},
		{0, -1, phi}, {0, 1, phi}, {0, -1, -phi}, {0, 1, -phi},
		{phi, 0, -1}, {phi, 0, 1}, {-phi, 0, -1}, {-phi, 0, 1},
	}
	for i, v := range raw {
		verts[i] = normalize3(v)
	}
	for f, tri := range icoFaces {
		a, b, c := verts[tri[0]], verts[tri[1]], verts[tri[2]]
		center := normalize3(add3(add3(a, b), c))
		// xAxis points from the face center towards vertex a,
		// projected into the tangent plane.
		xAxis := normalize3(sub3(a, scale3(center, dot3(a, center))))
		yAxis := cross3(center, xAxis)
		// Circumradius of the projected triangle: gnomonic
		// projection of vertex a onto the tangent plane.
		d := dot3(a, center)
		pa := sub3(scale3(a, 1/d), center)
		rho := math.Sqrt(dot3(pa, pa))
		faces[f] = faceGeom{center: center, xAxis: xAxis, yAxis: yAxis, rho: rho}
	}
	// Inward edge lines of each face's projected triangle.
	for f := 0; f < numFaces; f++ {
		var w [3][2]float64
		for m, vi := range icoFaces[f] {
			v := verts[vi]
			px, py := projectSphereToFace(f, v)
			w[m] = [2]float64{px, py}
		}
		for e := 0; e < 3; e++ {
			a := w[(e+1)%3]
			b := w[(e+2)%3]
			opp := w[e]
			dx, dy := b[0]-a[0], b[1]-a[1]
			l := math.Hypot(dx, dy)
			// Two candidate normals; keep the one pointing at the
			// opposite vertex (inwards).
			nx, ny := -dy/l, dx/l
			if nx*(opp[0]-a[0])+ny*(opp[1]-a[1]) < 0 {
				nx, ny = -nx, -ny
			}
			faceEdges[f][e] = edgeLine{nx: nx, ny: ny, c: -(nx*a[0] + ny*a[1])}
		}
	}
}

func add3(a, b [3]float64) [3]float64           { return [3]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func sub3(a, b [3]float64) [3]float64           { return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func scale3(a [3]float64, s float64) [3]float64 { return [3]float64{a[0] * s, a[1] * s, a[2] * s} }
func dot3(a, b [3]float64) float64              { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross3(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func normalize3(a [3]float64) [3]float64 { return scale3(a, 1/math.Sqrt(dot3(a, a))) }

// seamDistance returns the signed distance between a cell's center
// and the nearest edge of its face triangle, in face-plane units.
// It is negative for margin cells outside the triangle.
func seamDistance(f, res, i, j int) float64 {
	n := 1 << uint(res)
	size := hexSize(f, res)
	q := float64(i) - float64(n)/2
	r := float64(j) - float64(n)/2
	if n == 1 {
		q, r = 0, 0
	}
	px, py := axialToPlane(q, r, size)
	d := math.Inf(1)
	for _, e := range faceEdges[f] {
		if v := e.nx*px + e.ny*py + e.c; v < d {
			d = v
		}
	}
	return d
}

// hexSize returns the hexagon size (center-to-vertex scale of the
// pointy-top axial layout) used on a face at the given resolution.
// The factor 1.5*rho/n keeps the whole face triangle strictly inside
// the [0, n) x [0, n) grid for every supported resolution.
func hexSize(f int, res int) float64 {
	return 1.5 * faces[f].rho / float64(int(1)<<uint(res))
}

// projectSphereToFace gnomonically projects the unit vector p into
// the tangent plane of face f, returning plane coordinates.
func projectSphereToFace(f int, p [3]float64) (px, py float64) {
	g := &faces[f]
	d := dot3(p, g.center)
	qx := p[0]/d - g.center[0]
	qy := p[1]/d - g.center[1]
	qz := p[2]/d - g.center[2]
	return qx*g.xAxis[0] + qy*g.xAxis[1] + qz*g.xAxis[2],
		qx*g.yAxis[0] + qy*g.yAxis[1] + qz*g.yAxis[2]
}

// nearestFace returns the index of the face whose center is
// angularly closest to the unit vector p.
func nearestFace(p [3]float64) int {
	best, bestDot := 0, -2.0
	for f := 0; f < numFaces; f++ {
		if d := dot3(p, faces[f].center); d > bestDot {
			best, bestDot = f, d
		}
	}
	return best
}

var sqrt3 = math.Sqrt(3)

// planeToAxial converts face-plane coordinates to fractional axial
// hex coordinates (pointy-top layout with the given hex size).
func planeToAxial(px, py, size float64) (q, r float64) {
	return (sqrt3/3*px - 1.0/3*py) / size, (2.0 / 3 * py) / size
}

// axialToPlane converts fractional axial hex coordinates to
// face-plane coordinates.
func axialToPlane(q, r, size float64) (px, py float64) {
	return size * sqrt3 * (q + r/2), size * 1.5 * r
}

// cubeRound rounds fractional axial coordinates to the nearest
// integer hex using cube-coordinate rounding.
func cubeRound(q, r float64) (int, int) {
	x, z := q, r
	y := -x - z
	rx, ry, rz := math.Round(x), math.Round(y), math.Round(z)
	dx, dy, dz := math.Abs(rx-x), math.Abs(ry-y), math.Abs(rz-z)
	switch {
	case dx > dy && dx > dz:
		rx = -ry - rz
	case dy > dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return int(rx), int(rz)
}

// cellAtSpherePoint quantizes a unit sphere vector to the cell at
// the given resolution. It performs no trigonometry: only vector
// algebra, one square root per normalization, and cube rounding.
func cellAtSpherePoint(p [3]float64, res int) Cell {
	f := nearestFace(p)
	px, py := projectSphereToFace(f, p)
	q, r := planeToAxial(px, py, hexSize(f, res))
	iq, ir := cubeRound(q, r)
	n := 1 << uint(res)
	i, j := iq+n/2, ir+n/2
	// Defensive clamp: with the chosen hex size the face triangle
	// always quantizes strictly inside the grid, so this only
	// guards against floating-point ties on triangle edges.
	if i < 0 {
		i = 0
	} else if i >= n {
		i = n - 1
	}
	if j < 0 {
		j = 0
	} else if j >= n {
		j = n - 1
	}
	return newCell(f, res, i, j)
}

// cellCenterSphere returns the unit sphere vector of the center of
// the (possibly off-grid) axial position (i, j) on face f. Out-of
// range coordinates are meaningful: they linearly extend the face
// grid, which is what K-ring face crossing relies on. No trig.
func cellCenterSphere(f, res, i, j int) [3]float64 {
	n := 1 << uint(res)
	size := hexSize(f, res)
	q := float64(i) - float64(n)/2
	r := float64(j) - float64(n)/2
	if n == 1 && i == 0 && j == 0 {
		// At res 0 the single cell covers the whole face; its
		// center is the face center, not the grid-square center.
		// Off-grid neighbor positions still extend linearly.
		q, r = 0, 0
	}
	px, py := axialToPlane(q, r, size)
	g := &faces[f]
	return normalize3([3]float64{
		g.center[0] + px*g.xAxis[0] + py*g.yAxis[0],
		g.center[1] + px*g.xAxis[1] + py*g.yAxis[1],
		g.center[2] + px*g.xAxis[2] + py*g.yAxis[2],
	})
}
