package hexgrid

import "math"

// Hierarchical hexagon addressing on a face plane.
//
// Each face carries a pointy-top axial (q, r) hexagon lattice. The hierarchy
// uses aperture 7: every parent cell has 7 children (center + 6 neighbours).
// Refinement by one level applies the linear map
//
//	M = | 3  1 |      det(M) = 7
//	    |-1  2 |
//
// to axial coordinates, which is a rotation by theta = atan2(-sqrt(3)/2, 5/2)
// composed with a sqrt(7) scaling. A cell address is a root offset (one of 7
// base hexagons per face) plus one 3-bit digit per resolution level, each
// digit selecting one of the 7 residue classes of Z^2 / M*Z^2.
//
// All of the above is exact integer arithmetic: K-ring traversal, parent/child
// scaling and digit reduction never touch floating point or trigonometry.

const sqrt3 = 1.7320508075688772935

// init runs the geometry setup in dependency order: icosahedron faces
// first (faceVertexDist), then the hexagon lattice sizing derived from it.
func init() {
	initFaces()
	initGrid()
}

// axialOffsets are the 7 child offsets (digit 0..6); digit 0 is the center
// child. The same set serves as the 7 root hexagons per face.
var axialOffsets = [7][2]int64{
	{0, 0}, {1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1},
}

// digitForResidue maps f = (2q - r) mod 7 to the digit whose offset o makes
// (q, r) - o lie in the sublattice M*Z^2. The offsets above produce residues
// {0,2,3,1,5,4,6} respectively, i.e. a complete residue system mod M.
var digitForResidue = [7]int{0, 3, 1, 2, 5, 4, 6}

// ringDirs are the 6 axial neighbour directions in rotational order.
var ringDirs = [6][2]int64{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}

var (
	// hexSize[r] is the hexagon circumradius at resolution r, in face-plane
	// units (the unit sphere has radius 1).
	hexSize [maxRes + 1]float64
	// cosTheta/sinTheta rotate face-plane coordinates into the (per-level
	// rotated) hexagon lattice frame.
	cosTheta [maxRes + 1]float64
	sinTheta [maxRes + 1]float64
)

func initGrid() {
	// Root hexagons: 7 per face must cover the whole face triangle, which
	// holds when 1.5*sqrt(3)*size >= faceVertexDist; 0.5 gives margin.
	rootSize := 0.5 * faceVertexDist
	theta := math.Atan2(-sqrt3/2, 2.5) // rotation of the aperture-7 map M
	inv := 1.0
	for r := 0; r <= maxRes; r++ {
		hexSize[r] = rootSize * inv
		inv /= math.Sqrt(7)
		a := -float64(r) * theta
		cosTheta[r] = math.Cos(a)
		sinTheta[r] = math.Sin(a)
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func mod7(v int64) int64 {
	r := v % 7
	if r < 0 {
		r += 7
	}
	return r
}

// axialRound rounds fractional axial coordinates to the nearest hexagon
// using cube-coordinate rounding.
func axialRound(q, r float64) (int64, int64) {
	x := q
	z := r
	y := -x - z
	rx := math.Round(x)
	ry := math.Round(y)
	rz := math.Round(z)
	dx := math.Abs(rx - x)
	dy := math.Abs(ry - y)
	dz := math.Abs(rz - z)
	switch {
	case dx > dy && dx > dz:
		rx = -ry - rz
	case dy > dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return int64(rx), int64(rz)
}

// reduceAxial decomposes absolute level-res axial coordinates into a root
// offset (index into axialOffsets, -1 when outside the 7 root hexagons) and
// per-level digits. Pure integer arithmetic.
func reduceAxial(q, r int64, res int) (root int, q0, r0 int64, digits [numDigits]uint8) {
	for level := res; level >= 1; level-- {
		d := digitForResidue[mod7(2*q-r)]
		o := axialOffsets[d]
		dq := q - o[0]
		dr := r - o[1]
		q = (2*dq - dr) / 7 // M^-1 = 1/7 * | 2 -1 |
		r = (dq + 3*dr) / 7 //            | 1  3 |
		digits[level-1] = uint8(d)
	}
	root = -1
	for i := 0; i < 7; i++ {
		if axialOffsets[i][0] == q && axialOffsets[i][1] == r {
			root = i
			break
		}
	}
	return root, q, r, digits
}

// buildAxial composes a root offset and per-level digits into absolute
// level-res axial coordinates. Exact inverse of reduceAxial.
func buildAxial(root int, digits [numDigits]uint8, res int) (q, r int64) {
	q = axialOffsets[root][0]
	r = axialOffsets[root][1]
	for i := 0; i < res; i++ {
		o := axialOffsets[digits[i]]
		nq := 3*q + r + o[0]  // M = | 3  1 |
		nr := -q + 2*r + o[1] //   | -1 2 |
		q, r = nq, nr
	}
	return q, r
}

// nearestRoot returns the root hexagon closest to (q, r); used only as a
// safety clamp for points that round outside the 7 root hexagons.
func nearestRoot(q, r int64) int {
	best := 0
	var bestDist int64 = 1<<62 - 1
	for i := 0; i < 7; i++ {
		dq := q - axialOffsets[i][0]
		dr := r - axialOffsets[i][1]
		d := (abs64(dq) + abs64(dr) + abs64(dq+dr)) / 2
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// cartToAxial converts face-plane coordinates to axial coordinates of the
// resolution-res lattice (rotating into the lattice frame first).
func cartToAxial(x, y float64, res int) (int64, int64) {
	cosT := cosTheta[res]
	sinT := sinTheta[res]
	xr := x*cosT + y*sinT
	yr := -x*sinT + y*cosT
	s := hexSize[res]
	xf := xr / (s * sqrt3)
	yf := yr / (1.5 * s)
	return axialRound(xf-yf/2, yf)
}

// axialToCartBase converts absolute level-res axial coordinates back to
// face-plane coordinates.
func axialToCartBase(q, r int64, res int) (x, y float64) {
	s := hexSize[res]
	xg := s * sqrt3 * (float64(q) + 0.5*float64(r))
	yg := s * 1.5 * float64(r)
	cosT := cosTheta[res]
	sinT := sinTheta[res]
	return xg*cosT - yg*sinT, xg*sinT + yg*cosT
}
