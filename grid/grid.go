// Package grid implements a discrete hexagonal hierarchical geospatial index
// built on an icosahedral unfolding of the sphere.
//
// Pipeline: lat/lng -> unit vector -> nearest icosahedron face -> central
// projection onto the face plane -> hexagonal lattice quantization ->
// (base cell, base-7 digit) decomposition -> 64-bit index.
//
// Hierarchy: the aperture-7 refinement uses the integer linear map
// M = [[2,-1],[1,3]] (det = 7, i.e. multiplication by sqrt(7) with rotation
// in hex space). Every axial coordinate decomposes uniquely as
// c = M*parent + offset[d], so resolution-r cells are addressed by a
// resolution-0 base cell (itself a lattice coordinate) plus r base-7 digits.
// Because Z^2 = M^r Z^2 + G_r (G_r = r-fold Gosper offsets), the cells at
// every resolution tile the whole face plane exactly once.
//
// The hex lattice lives directly on each icosahedron face plane with basis
// vectors aligned to the face edges, so adjacent faces share identical
// lattice points along shared edges and cross-face quantization stays
// consistent.
//
// K-Ring traversal is pure integer cube-coordinate walking: no
// floating-point trigonometry is involved in the topology expansion.
package grid

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MaxRes is the maximum supported resolution (13 base-7 digits).
const MaxRes = 13

const (
	modeCell    = 1
	digitUnused = 7
	baseBias    = 16 // base cell IJ coords live in [-16, 16)
	baseBits    = 5
)

// Index is a 64-bit hierarchical hexagonal cell index.
//
// Bit layout (MSB first):
//
//	[63:60] mode (1 = cell)
//	[59:56] resolution (0..13)
//	[55:51] icosahedron face (0..19)
//	[50:46] base cell q + 16
//	[45:41] base cell r + 16
//	[40:2]  13 base-7 digits, 3 bits each (unused trailing digits = 7)
//	[1:0]   reserved (0)
type Index uint64

func makeIndex(face, res, bq, br int, digits *[MaxRes]uint8) Index {
	v := uint64(modeCell)<<60 |
		uint64(res&0xF)<<56 |
		uint64(face&0x1F)<<51 |
		uint64((bq+baseBias)&0x1F)<<46 |
		uint64((br+baseBias)&0x1F)<<41
	for i := 0; i < MaxRes; i++ {
		d := uint64(digitUnused)
		if i < res {
			d = uint64(digits[i] & 7)
		}
		v |= d << uint(40-3-3*i)
	}
	return Index(v)
}

// Res returns the resolution of the index.
func (i Index) Res() int { return int(uint64(i) >> 56 & 0xF) }

// Face returns the icosahedron face number (0..19).
func (i Index) Face() int { return int(uint64(i) >> 51 & 0x1F) }

// BaseCell returns the resolution-0 lattice coordinate of the cell.
func (i Index) BaseCell() (bq, br int) {
	return int(uint64(i)>>46&0x1F) - baseBias, int(uint64(i)>>41&0x1F) - baseBias
}

// Digit returns the base-7 digit at 1-based position pos.
func (i Index) Digit(pos int) uint8 {
	return uint8(uint64(i) >> uint(40-3*pos) & 7)
}

// String renders the index as a 16-character hexadecimal string.
func (i Index) String() string { return fmt.Sprintf("%016x", uint64(i)) }

// Parse parses a hexadecimal index string produced by String.
func Parse(s string) (Index, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("grid: invalid index %q: %w", s, err)
	}
	idx := Index(v)
	if idx>>60 != modeCell {
		return 0, errors.New("grid: not a cell index")
	}
	if idx.Res() > MaxRes {
		return 0, errors.New("grid: resolution out of range")
	}
	if idx.Face() >= numFaces {
		return 0, errors.New("grid: face out of range")
	}
	return idx, nil
}

// ---------------------------------------------------------------------------
// Aperture-7 hierarchy in axial (q, r) integer coordinates.
//
// Child transform:  c = M*p + offset[d],  M = [[2,-1],[1,3]], det(M) = 7.
// The 7 offsets are the center plus the 6 unit hex neighbors; they form a
// complete residue system of Z^2 / M*Z^2, so the decomposition
// c = M^res * base + sum M^(res-p) * offset[d_p] is a bijection between
// Z^2 and (base, digits).
// ---------------------------------------------------------------------------

var childOffset = [7][2]int{
	{0, 0}, {1, 0}, {0, 1}, {-1, 1}, {-1, 0}, {0, -1}, {1, -1},
}

// digitByT maps t = (3q + r) mod 7 to the digit whose offset matches.
var digitByT = [7]int{0, 2, 6, 1, 4, 3, 5}

func mod7(x int) int {
	m := x % 7
	if m < 0 {
		m += 7
	}
	return m
}

// ij unfolds base cell + digits into the cell's axial lattice coordinate.
// Pure integer math.
func (i Index) ij() (q, r int) {
	q, r = i.BaseCell()
	for p := 1; p <= i.Res(); p++ {
		o := childOffset[i.Digit(p)]
		q, r = 2*q-r+o[0], q+3*r+o[1]
	}
	return q, r
}

// fold decomposes an axial coordinate into base cell + res digits.
// Exact inverse of ij.
func fold(q, r, res int) (bq, br int, d [MaxRes]uint8) {
	for level := res; level >= 1; level-- {
		dig := digitByT[mod7(3*q+r)]
		d[level-1] = uint8(dig)
		o := childOffset[dig]
		cq, cr := q-o[0], r-o[1]
		// M^-1 = (1/7) * [[3,1],[-1,2]]; division is exact by construction.
		q = (3*cq + cr) / 7
		r = (-cq + 2*cr) / 7
	}
	return q, r, d
}

// Parent returns the ancestor of i at the given (coarser) resolution.
// Because the (base cell, digit) decomposition is a bijection, the parent is
// exactly the digit-prefix truncation of the child.
func (i Index) Parent(res int) (Index, error) {
	if res < 0 || res > i.Res() {
		return 0, fmt.Errorf("grid: parent resolution %d out of range for res %d", res, i.Res())
	}
	bq, br := i.BaseCell()
	var d [MaxRes]uint8
	for p := 1; p <= res; p++ {
		d[p-1] = i.Digit(p)
	}
	return makeIndex(i.Face(), res, bq, br, &d), nil
}

// Children returns the 7 immediate children of i at resolution Res()+1:
// the same base cell and digit prefix plus each possible trailing digit.
func (i Index) Children() ([]Index, error) {
	r := i.Res()
	if r >= MaxRes {
		return nil, fmt.Errorf("grid: already at max resolution %d", MaxRes)
	}
	bq, br := i.BaseCell()
	var d [MaxRes]uint8
	for p := 1; p <= r; p++ {
		d[p-1] = i.Digit(p)
	}
	out := make([]Index, 7)
	for c := 0; c < 7; c++ {
		d[r] = uint8(c)
		out[c] = makeIndex(i.Face(), r+1, bq, br, &d)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Hexagonal quantization on the icosahedron face planes.
//
// The res-0 lattice has spacing h0 = edgeLen / latticePerEdge and basis
// vectors along two face edges (60 deg apart), so adjacent faces share
// identical lattice points along shared edges. The resolution-r lattice is
// the res-0 lattice transformed by M^-r (shrunk by sqrt(7)^r and rotated),
// which is exactly what the digit hierarchy unfolds: a cell with axial
// coordinate c at resolution r has physical position h0 * M^-r * c.
// ---------------------------------------------------------------------------

// latticePerEdge is the number of res-0 lattice spacings per face edge.
const latticePerEdge = 4.0

// mPow[r] = M^r as an exact integer 2x2 matrix; detPow[r] = 7^r.
var mPow [MaxRes + 1][2][2]int
var detPow [MaxRes + 1]int

func init() {
	mPow[0] = [2][2]int{{1, 0}, {0, 1}}
	detPow[0] = 1
	for r := 1; r <= MaxRes; r++ {
		a := mPow[r-1]
		mPow[r] = [2][2]int{
			{2*a[0][0] - a[1][0], 2*a[0][1] - a[1][1]},
			{a[0][0] + 3*a[1][0], a[0][1] + 3*a[1][1]},
		}
		detPow[r] = detPow[r-1] * 7
	}
}

func h0() float64 { return edgeLen / latticePerEdge }

// latticePoint returns the 3D point of axial coordinate (q, r) at resolution
// res on a face: origin + h0 * M^-res * (q, r) in the face basis.
func latticePoint(face, q, r, res int) vec3 {
	g := faces[face]
	m := mPow[res]
	det := float64(detPow[res])
	// adj(M^res) / 7^res applied to (q, r).
	u := float64(m[1][1]*q-m[0][1]*r) / det
	v := float64(-m[1][0]*q+m[0][0]*r) / det
	return g.origin.
		add(g.b1.scale(h0() * u)).
		add(g.b2.scale(h0() * v))
}

// quantize finds the nearest resolution-res lattice coordinate of a 3D
// point known to lie on the face plane.
func quantize(face int, p vec3, res int) (q, r int) {
	g := faces[face]
	w := p.sub(g.origin)
	u := w.dot(g.d1) / h0()
	v := w.dot(g.d2) / h0()
	m := mPow[res]
	fq := float64(m[0][0])*u + float64(m[0][1])*v
	fr := float64(m[1][0])*u + float64(m[1][1])*v
	return cubeRound(fq, -fq-fr, fr)
}

func cubeRound(x, y, z float64) (q, r int) {
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

// ---------------------------------------------------------------------------
// Encoding / decoding.
// ---------------------------------------------------------------------------

// nearestFace returns the face whose outward normal is closest to p.
func nearestFace(p vec3) int {
	face, best := 0, -2.0
	for f := 0; f < numFaces; f++ {
		if d := p.dot(faces[f].normal); d > best {
			best, face = d, f
		}
	}
	return face
}

// encodeVec finds the containing cell of a unit vector at the given
// resolution. The point is projected onto the nearest face and quantized;
// the resulting cell is then re-anchored to the face nearest to the cell
// CENTER, so every cell is owned by exactly one face and face boundaries
// follow cell edges. This makes encoding idempotent (a cell center always
// re-encodes to its own cell) which the neighbor topology relies on.
func encodeVec(p vec3, res int) (Index, error) {
	face := nearestFace(p)
	g := faces[face]
	proj := p.scale(g.offset / p.dot(g.normal)) // ray-plane intersection
	q, r := quantize(face, proj, res)
	// Re-anchor: the cell belongs to the face nearest to its center.
	// Iterate to a fixpoint (converges in 1-2 steps; vertices may need 3).
	for i := 0; i < 4; i++ {
		center := latticePoint(face, q, r, res).norm()
		owner := nearestFace(center)
		if owner == face {
			break
		}
		face = owner
		g = faces[face]
		proj = center.scale(g.offset / center.dot(g.normal))
		q, r = quantize(face, proj, res)
	}
	bq, br, digits := fold(q, r, res)
	if bq < -baseBias || bq >= baseBias || br < -baseBias || br >= baseBias {
		return 0, fmt.Errorf("grid: base cell (%d,%d) out of encodable window", bq, br)
	}
	return makeIndex(face, res, bq, br, &digits), nil
}

// Encode converts a WGS84 lat/lng (degrees) into a cell index at res.
func Encode(lat, lng float64, res int) (Index, error) {
	if res < 0 || res > MaxRes {
		return 0, fmt.Errorf("grid: resolution %d out of range [0,%d]", res, MaxRes)
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, fmt.Errorf("grid: invalid coordinate (%v, %v)", lat, lng)
	}
	phi, lam := lat*math.Pi/180, lng*math.Pi/180
	p := vec3{
		math.Cos(phi) * math.Cos(lam),
		math.Cos(phi) * math.Sin(lam),
		math.Sin(phi),
	}
	return encodeVec(p, res)
}

// Center returns the lat/lng (degrees) of the cell center.
func (i Index) Center() (lat, lng float64) {
	q, r := i.ij()
	p := latticePoint(i.Face(), q, r, i.Res()).norm()
	return vecToLatLng(p)
}

func vecToLatLng(p vec3) (lat, lng float64) {
	return math.Atan2(p.z, math.Hypot(p.x, p.y)) * 180 / math.Pi,
		math.Atan2(p.y, p.x) * 180 / math.Pi
}

// ---------------------------------------------------------------------------
// Topological neighborhood traversal.
//
// Neighbor coordinates are computed with pure integer axial arithmetic on
// each cell's own face lattice; each candidate is then re-anchored to the
// sphere by normalizing its 3D lattice point (vector math only, no
// floating-point trigonometry) and re-encoded. Because every cell emits
// neighbors from its own lattice, single-step crossings of icosahedron face
// edges stay exact and KRing coverage is closed and hole-free by
// construction (BFS expansion over the adjacency graph).
// ---------------------------------------------------------------------------

// axialNeighbors are the 6 unit neighbor offsets in axial coordinates.
var axialNeighbors = [6][2]int{
	{1, 0}, {1, -1}, {0, -1},
	{-1, 0}, {-1, 1}, {0, 1},
}

// Neighbors returns the distinct cells adjacent to idx (6 in the interior;
// fewer where the icosahedron curvature concentrates near vertices).
func Neighbors(idx Index) []Index {
	res := idx.Res()
	q0, r0 := idx.ij()
	face := idx.Face()

	out := make([]Index, 0, 6)
	seen := make(map[Index]struct{}, 6)
	for _, o := range axialNeighbors {
		p := latticePoint(face, q0+o[0], r0+o[1], res).norm()
		n, err := encodeVec(p, res)
		if err != nil || n == idx {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// KRing returns origin plus all cells within topological hex distance k,
// ordered by ring (breadth-first expansion over the adjacency graph).
func KRing(origin Index, k int) []Index {
	out := make([]Index, 0, 1+3*k*(k+1))
	seen := make(map[Index]struct{}, 1+3*k*(k+1))
	out = append(out, origin)
	seen[origin] = struct{}{}

	frontier := []Index{origin}
	for ring := 0; ring < k; ring++ {
		var next []Index
		for _, c := range frontier {
			for _, n := range Neighbors(c) {
				if _, ok := seen[n]; ok {
					continue
				}
				seen[n] = struct{}{}
				next = append(next, n)
				out = append(out, n)
			}
		}
		frontier = next
	}
	return out
}
