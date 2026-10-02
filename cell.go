package hexgrid

import (
	"errors"
	"math"
	"strconv"
)

// Cell is a 64-bit hierarchical hexagonal grid index.
//
// Bit layout (MSB first):
//
//	[63:59] face       5 bits  icosahedron face, 0..19
//	[58:55] resolution 4 bits  0..15
//	[54:28] q         27 bits  axial coord q = i-k, biased by 1<<26
//	[27: 1] r         27 bits  axial coord r = j-k, biased by 1<<26
//	[    0] reserved  1 bit    always 0
//
// The (q, r) pair is a bijection with normalized ijk coordinates, so every
// cell on a face has a unique encoding.
type Cell uint64

const (
	faceShift = 59
	resShift  = 55
	qShift    = 28
	rShift    = 1
	coordMask = 1<<27 - 1
	coordBias = 1 << 26
)

// ErrInvalidCell is returned when parsing a malformed cell string.
var ErrInvalidCell = errors.New("hexgrid: invalid cell")

// encode builds a Cell from its parts. q and r are the unbiased axial
// coordinates (q = i-k, r = j-k of the normalized ijk coordinate).
func encodeCell(face, res, q, r int) Cell {
	return Cell(uint64(face)<<faceShift |
		uint64(res)<<resShift |
		uint64(q+coordBias)<<qShift |
		uint64(r+coordBias)<<rShift)
}

// decode splits a Cell into face, resolution and normalized ijk coordinate.
func decodeCell(c Cell) (face, res int, coord ijk) {
	u := uint64(c)
	face = int(u >> faceShift)
	res = int((u >> resShift) & 0xf)
	q := int((u>>qShift)&coordMask) - coordBias
	r := int((u>>rShift)&coordMask) - coordBias
	coord = normalizeIJK(ijk{q, r, 0})
	return
}

// CellFromLatLng encodes a geographic coordinate (degrees) at the given
// resolution into a Cell. Trigonometry is confined to this entry point (and
// its inverse); topological traversal never uses it.
func CellFromLatLng(lat, lng float64, res int) Cell {
	if res < 0 || res > maxRes {
		panic("hexgrid: resolution out of range")
	}
	phi := lat * math.Pi / 180
	lambda := lng * math.Pi / 180
	p := vec3{
		math.Cos(phi) * math.Cos(lambda),
		math.Cos(phi) * math.Sin(lambda),
		math.Sin(phi),
	}
	f := bestFace(p)
	g := faces[f]
	t := 1 / p.dot(g.n)
	x := t * p.dot(g.u) / scaleByRes[res]
	y := t * p.dot(g.v) / scaleByRes[res]
	coord := hex2dToIJK(x, y)
	// Normalize ownership: if the quantized cell's center falls outside
	// this face's gnomonic triangle, re-project it onto the face that
	// owns the center. This makes encode(LatLng(cell)) == cell hold
	// everywhere, including near face edges.
	f2, coord2 := localize(f, res, coord)
	return encodeIJK(f2, res, coord2)
}

// encodeIJK encodes a normalized ijk coordinate on a face into a Cell.
func encodeIJK(face, res int, c ijk) Cell {
	return encodeCell(face, res, c.i-c.k, c.j-c.k)
}

// LatLng returns the geographic center of the cell in degrees.
func (c Cell) LatLng() (lat, lng float64) {
	face, res, coord := decodeCell(c)
	x, y := ijkToHex2d(coord)
	x *= scaleByRes[res]
	y *= scaleByRes[res]
	g := faces[face]
	dir := g.n.add(g.u.scale(x)).add(g.v.scale(y)).unit()
	lat = math.Atan2(dir.z, math.Hypot(dir.x, dir.y)) * 180 / math.Pi
	lng = math.Atan2(dir.y, dir.x) * 180 / math.Pi
	return
}

// Face returns the icosahedron face index (0..19) the cell lies on.
func (c Cell) Face() int { return int(uint64(c) >> faceShift) }

// Resolution returns the cell resolution (0..15).
func (c Cell) Resolution() int { return int((uint64(c) >> resShift) & 0xf) }

// Parent returns the cell's parent at the given (coarser) resolution.
// The hierarchy is defined geometrically: the parent is the unique cell at
// the requested resolution whose area contains this cell's center.
func (c Cell) Parent(res int) Cell {
	if res < 0 || res > c.Resolution() {
		panic("hexgrid: invalid parent resolution")
	}
	if res == c.Resolution() {
		return c
	}
	lat, lng := c.LatLng()
	return CellFromLatLng(lat, lng, res)
}

// Children returns all cells at resolution res (finer than the cell's own)
// whose parent chain leads back to this cell. For non-pentagonal regions a
// cell one resolution down has exactly 7 children. Because the hierarchy is
// geometric (each resolution is an independent sqrt(7) scaling), children
// are the finer cells whose centers fall inside this cell, gathered from a
// small candidate disk around the center child.
func (c Cell) Children(res int) []Cell {
	if res <= c.Resolution() || res > maxRes {
		return nil
	}
	cur := []Cell{c}
	for r := c.Resolution() + 1; r <= res; r++ {
		var next []Cell
		var buf [64]Cell
		for _, p := range cur {
			lat, lng := p.LatLng()
			center := CellFromLatLng(lat, lng, r)
			n := GridDiskInto(center, 3, buf[:])
			for i := 0; i < n; i++ {
				if buf[i].Parent(r-1) == p {
					next = append(next, buf[i])
				}
			}
		}
		cur = next
	}
	return cur
}

// String renders the cell as a 16-digit hexadecimal string.
func (c Cell) String() string {
	const digits = "0123456789abcdef"
	var b [16]byte
	u := uint64(c)
	for i := 15; i >= 0; i-- {
		b[i] = digits[u&0xf]
		u >>= 4
	}
	return string(b[:])
}

// ParseCell parses a 16-digit hexadecimal cell string.
func ParseCell(s string) (Cell, error) {
	if len(s) != 16 {
		return 0, ErrInvalidCell
	}
	u, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, ErrInvalidCell
	}
	c := Cell(u)
	if c.Face() >= 20 || u&1 != 0 {
		return 0, ErrInvalidCell
	}
	return c, nil
}

// FaceCenterLatLng returns the geographic coordinates (degrees) of an
// icosahedron face center. Useful for tests and demos that need a point
// safely away from face edges.
func FaceCenterLatLng(face int) (lat, lng float64) {
	n := faceCenter(face)
	lat = math.Atan2(n.z, math.Hypot(n.x, n.y)) * 180 / math.Pi
	lng = math.Atan2(n.y, n.x) * 180 / math.Pi
	return
}
