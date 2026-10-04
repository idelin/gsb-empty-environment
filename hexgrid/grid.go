// Package hexgrid implements a discrete hexagonal hierarchical spatial index
// unfolded over an icosahedron, together with integer-only topological
// k-ring neighbourhood traversal.
//
// Index layout (uint64):
//
//	bits 63..60  mode        (1 = cell)
//	bits 59..56  resolution  (0..15)
//	bits 55..51  icosahedron face (0..19)
//	bits 50..34  axial i + bias (17 bits)
//	bits 33..17  axial j + bias (17 bits)
//	bits 16..0   reserved (0)
//
// LatLng -> Cell encoding uses a gnomonic projection onto the nearest
// icosahedron face (this is the only place trigonometry appears).
// KRing / Neighbors / Parent are pure integer arithmetic on cube/axial
// hexagon coordinates: no floating point, no trigonometry.
package hexgrid

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

const (
	// MaxResolution is the finest supported hierarchy level.
	MaxResolution = 15

	numFaces = 20
	ijBias   = 1 << 16
	ijMask   = (1 << 17) - 1
	modeCell = 1

	// faceScale controls how many hexagons span one icosahedron face at
	// resolution 0. Hexagon circumradius at resolution r is
	// faceScale / 2^r in face-local coordinates.
	faceScale = 0.9
)

// ErrInvalidCell is returned when parsing a malformed cell string.
var ErrInvalidCell = errors.New("hexgrid: invalid cell")

// Cell is a 64-bit hierarchical hexagonal cell index.
type Cell uint64

// ---------------------------------------------------------------------------
// Icosahedron geometry (initialised once, read-only afterwards)
// ---------------------------------------------------------------------------

type vec3 struct{ x, y, z float64 }

func (v vec3) add(o vec3) vec3      { return vec3{v.x + o.x, v.y + o.y, v.z + o.z} }
func (v vec3) sub(o vec3) vec3      { return vec3{v.x - o.x, v.y - o.y, v.z - o.z} }
func (v vec3) scale(s float64) vec3 { return vec3{v.x * s, v.y * s, v.z * s} }
func (v vec3) dot(o vec3) float64   { return v.x*o.x + v.y*o.y + v.z*o.z }
func (v vec3) cross(o vec3) vec3 {
	return vec3{v.y*o.z - v.z*o.y, v.z*o.x - v.x*o.z, v.x*o.y - v.y*o.x}
}
func (v vec3) norm() vec3 {
	l := math.Sqrt(v.dot(v))
	return vec3{v.x / l, v.y / l, v.z / l}
}

var phi = (1 + math.Sqrt(5)) / 2

// IcoVertices are the 12 vertices of a regular icosahedron with edge length 2.
var IcoVertices = []vec3{
	{0, 1, phi}, {0, 1, -phi}, {0, -1, phi}, {0, -1, -phi},
	{1, phi, 0}, {1, -phi, 0}, {-1, phi, 0}, {-1, -phi, 0},
	{phi, 0, 1}, {-phi, 0, 1}, {phi, 0, -1}, {-phi, 0, -1},
}

// IcoFaces lists the 20 triangular faces as vertex index triples.
var IcoFaces = [][3]int{
	{0, 8, 4}, {0, 4, 6}, {0, 6, 9}, {0, 9, 2}, {0, 2, 8},
	{3, 11, 1}, {3, 1, 10}, {3, 10, 5}, {3, 5, 7}, {3, 7, 11},
	{4, 8, 10}, {8, 10, 5}, {8, 5, 2}, {2, 5, 7}, {2, 7, 9},
	{9, 7, 11}, {9, 11, 6}, {6, 11, 1}, {6, 1, 4}, {4, 1, 10},
}

type faceGeom struct {
	normal vec3 // outward unit normal of the face plane
	u, w   vec3 // orthonormal in-plane basis
	center vec3 // face centroid (lies on the plane)
}

var faceGeoms [numFaces]faceGeom

func init() {
	for f := 0; f < numFaces; f++ {
		a := IcoVertices[IcoFaces[f][0]]
		b := IcoVertices[IcoFaces[f][1]]
		c := IcoVertices[IcoFaces[f][2]]
		n := b.sub(a).cross(c.sub(a)).norm()
		centroid := a.add(b).add(c).scale(1.0 / 3.0)
		if n.dot(centroid) < 0 { // keep the normal pointing outward
			n = n.scale(-1)
		}
		u := b.sub(a).norm()
		w := n.cross(u)
		faceGeoms[f] = faceGeom{normal: n, u: u, w: w, center: centroid}
	}
}

// ---------------------------------------------------------------------------
// Bit packing
// ---------------------------------------------------------------------------

func makeCell(face, res, i, j int) Cell {
	u := uint64(modeCell)<<60 |
		uint64(res&0xF)<<56 |
		uint64(face&0x1F)<<51 |
		uint64((i+ijBias)&ijMask)<<34 |
		uint64((j+ijBias)&ijMask)<<17
	return Cell(u)
}

// Mode returns the index mode (1 for cells).
func (c Cell) Mode() int { return int(uint64(c) >> 60) }

// Resolution returns the hierarchy level of the cell (0..15).
func (c Cell) Resolution() int { return int(uint64(c)>>56) & 0xF }

// Face returns the icosahedron face (0..19) the cell lives on.
func (c Cell) Face() int { return int(uint64(c)>>51) & 0x1F }

// IJ returns the face-local axial hexagon coordinates of the cell.
func (c Cell) IJ() (i, j int) {
	i = int(uint64(c)>>34&ijMask) - ijBias
	j = int(uint64(c)>>17&ijMask) - ijBias
	return
}

// IsValid reports whether the cell has sane mode/resolution/face fields.
func (c Cell) IsValid() bool {
	return c.Mode() == modeCell && c.Resolution() <= MaxResolution && c.Face() < numFaces
}

// String renders the cell as a zero-padded 16 character hexadecimal string.
func (c Cell) String() string { return fmt.Sprintf("%016x", uint64(c)) }

// ParseCell decodes a hexadecimal cell string produced by String.
func ParseCell(s string) (Cell, error) {
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, ErrInvalidCell
	}
	c := Cell(u)
	if !c.IsValid() {
		return 0, ErrInvalidCell
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// LatLng <-> Cell (gonomic projection; the only float/trig code path)
// ---------------------------------------------------------------------------

// FromLatLng encodes a WGS84 coordinate (degrees) into the cell at the given
// resolution. It panics if res is outside [0, MaxResolution].
func FromLatLng(latDeg, lngDeg float64, res int) Cell {
	if res < 0 || res > MaxResolution {
		panic("hexgrid: resolution out of range")
	}
	lat := latDeg * math.Pi / 180
	lng := lngDeg * math.Pi / 180
	p := vec3{
		math.Cos(lat) * math.Cos(lng),
		math.Sin(lat),
		math.Cos(lat) * math.Sin(lng),
	}
	// Nearest face = largest dot product with the outward face normal.
	face := 0
	best := math.Inf(-1)
	for f := 0; f < numFaces; f++ {
		if d := p.dot(faceGeoms[f].normal); d > best {
			best, face = d, f
		}
	}
	g := faceGeoms[face]
	// Gnomonic projection onto the face plane {x : x.n = h}.
	h := g.center.dot(g.normal)
	proj := p.scale(h / p.dot(g.normal))
	rel := proj.sub(g.center)
	x := rel.dot(g.u)
	y := rel.dot(g.w)
	size := faceScale / float64(int(1)<<uint(res))
	// Pointy-top axial coordinates from cartesian, then cube-round.
	fq := (math.Sqrt(3)/3*x - 1.0/3.0*y) / size
	fr := (2.0 / 3.0 * y) / size
	i, j := cubeRoundAxial(fq, fr)
	return makeCell(face, res, i, j)
}

// CenterLatLng returns the spherical coordinates of the cell centre.
func (c Cell) CenterLatLng() (latDeg, lngDeg float64) {
	g := faceGeoms[c.Face()]
	size := faceScale / float64(int(1)<<uint(c.Resolution()))
	i, j := c.IJ()
	fi, fj := float64(i), float64(j)
	x := size * math.Sqrt(3) * (fi + fj/2)
	y := size * 1.5 * fj
	p := g.center.add(g.u.scale(x)).add(g.w.scale(y)).norm()
	latDeg = math.Asin(p.y) * 180 / math.Pi
	lngDeg = math.Atan2(p.z, p.x) * 180 / math.Pi
	return
}

// cubeRoundAxial rounds fractional axial coordinates to the nearest hexagon.
func cubeRoundAxial(fq, fr float64) (i, j int) {
	fx, fz := fq, fr
	fy := -fx - fz
	rx := math.Round(fx)
	ry := math.Round(fy)
	rz := math.Round(fz)
	dx := math.Abs(rx - fx)
	dy := math.Abs(ry - fy)
	dz := math.Abs(rz - fz)
	switch {
	case dx >= dy && dx >= dz:
		rx = -ry - rz
	case dy >= dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return int(rx), int(rz)
}

// ---------------------------------------------------------------------------
// Hierarchy: parent / children (pure integer cube halving)
// ---------------------------------------------------------------------------

// Parent returns the ancestor cell at the given (coarser) resolution.
// It panics if res is not strictly smaller than the cell's resolution.
func (c Cell) Parent(res int) Cell {
	r := c.Resolution()
	if res < 0 || res >= r {
		panic("hexgrid: parent resolution must be in [0, cell.Resolution())")
	}
	i, j := c.IJ()
	for rr := r; rr > res; rr-- {
		i, j = cubeHalveAxial(i, j)
	}
	return makeCell(c.Face(), res, i, j)
}

// Children enumerates the cells one resolution finer whose Parent is c.
// It returns nil at MaxResolution.
func (c Cell) Children() []Cell {
	r := c.Resolution()
	if r >= MaxResolution {
		return nil
	}
	i, j := c.IJ()
	var out []Cell
	for di := -1; di <= 1; di++ {
		for dj := -1; dj <= 1; dj++ {
			cand := makeCell(c.Face(), r+1, 2*i+di, 2*j+dj)
			if cand.Parent(r) == c {
				out = append(out, cand)
			}
		}
	}
	return out
}

// roundDiv2 rounds v/2 to the nearest integer, halves away from zero.
func roundDiv2(v int) int {
	if v >= 0 {
		return (v + 1) >> 1
	}
	return -((-v + 1) >> 1)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// cubeHalveAxial maps axial (i,j) to the coarse cell containing it when the
// grid is scaled by 1/2, using integer-only cube rounding.
func cubeHalveAxial(i, j int) (int, int) {
	x, z := i, j
	y := -x - z
	rx, ry, rz := roundDiv2(x), roundDiv2(y), roundDiv2(z)
	dx, dy, dz := abs(2*rx-x), abs(2*ry-y), abs(2*rz-z)
	switch {
	case dx >= dy && dx >= dz:
		rx = -ry - rz
	case dy >= dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return rx, rz
}

// ---------------------------------------------------------------------------
// Topology: neighbours, k-ring, distance (pure integer, no floats)
// ---------------------------------------------------------------------------

// axialDirs are the six axial neighbour offsets (cube dirs projected to (i,j)).
var axialDirs = [6][2]int{
	{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1},
}

// Neighbors returns the six topologically adjacent cells, starting with the
// +i direction and proceeding clockwise. All arithmetic is integer-only.
func (c Cell) Neighbors() [6]Cell {
	i, j := c.IJ()
	face, res := c.Face(), c.Resolution()
	var out [6]Cell
	for k := 0; k < 6; k++ {
		out[k] = makeCell(face, res, i+axialDirs[k][0], j+axialDirs[k][1])
	}
	return out
}

// KRing returns every cell within topological distance k of c, ordered as:
// the centre first, then ring 1, ring 2, ... ring k, each ring walked
// clockwise. The result contains exactly 1 + 3k(k+1) cells and is computed
// with integer arithmetic only.
func (c Cell) KRing(k int) []Cell {
	if k < 0 {
		panic("hexgrid: negative k")
	}
	face, res := c.Face(), c.Resolution()
	i, j := c.IJ()
	out := make([]Cell, 0, 1+3*k*(k+1))
	out = append(out, c)
	for d := 1; d <= k; d++ {
		// Start of ring d: walk d steps in direction 4 (-1, +1).
		ci, cj := i-d, j+d
		for dir := 0; dir < 6; dir++ {
			for step := 0; step < d; step++ {
				out = append(out, makeCell(face, res, ci, cj))
				ci += axialDirs[dir][0]
				cj += axialDirs[dir][1]
			}
		}
	}
	return out
}

// GridDistance returns the topological (hex grid) distance between two cells
// on the same face and resolution. ok is false if the cells are not
// comparable (different face or resolution).
func (c Cell) GridDistance(o Cell) (dist int, ok bool) {
	if c.Face() != o.Face() || c.Resolution() != o.Resolution() {
		return 0, false
	}
	ai, aj := c.IJ()
	bi, bj := o.IJ()
	dx := ai - bi
	dz := aj - bj
	dy := -dx - dz
	return (abs(dx) + abs(dy) + abs(dz)) / 2, true
}
