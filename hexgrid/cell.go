// Package hexgrid implements a discrete hexagonal geospatial grid built by
// gnomonic projection of the sphere onto the 20 faces of an icosahedron.
//
// A Cell is a 64-bit hierarchical identifier:
//
//	[63]     reserved (0)
//	[62:59]  mode (1 = cell)
//	[58:55]  resolution (0..15)
//	[54:50]  icosahedron face (0..19)
//	[49:25]  axial coordinate q (biased)
//	[24:0]   axial coordinate r (biased)
//
// The hexagon lattice constant at resolution r is 2^(1-r) in icosahedron
// edge units, so the lattice divides every face edge evenly and all 20 face
// lattices unfold into one globally consistent hexagonal lattice. A cell
// whose center lies exactly on a face boundary is canonically owned by the
// lowest-indexed face containing it, which keeps cell identities unique and
// adjacency symmetric across face edges. Each resolution level halves the
// lattice constant, so a cell has between 1 and 7 children (4 on average).
//
// The canonical string form is a 16-digit lowercase hexadecimal string.
package hexgrid

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// MaxResolution is the finest supported grid resolution.
const MaxResolution = 15

const (
	modeCell      = 1
	coordBias     = 1 << 24
	coordMask     = 0x1ffffff
	earthRadiusKm = 6371.0
)

// Cell is a 64-bit hierarchical hexagonal cell identifier.
type Cell uint64

// ErrInvalidResolution is returned (or panics through Encode) when a
// resolution outside [0, MaxResolution] is requested.
var ErrInvalidResolution = errors.New("hexgrid: invalid resolution")

// ErrInvalidCell is returned when parsing a malformed cell string.
var ErrInvalidCell = errors.New("hexgrid: invalid cell")

// hexSize is the hexagon circumradius in icosahedron 3D units at res. The
// icosahedron edge length is 2, so the lattice constant size*sqrt3 equals
// 2^(1-res) and divides every face edge exactly 2^res times.
func hexSize(res int) float64 {
	return math.Ldexp(2/sqrt3, -res)
}

func makeCell(res, faceIdx, q, r int) Cell {
	return Cell(uint64(modeCell)<<59 |
		uint64(res&0xf)<<55 |
		uint64(faceIdx&0x1f)<<50 |
		(uint64(q+coordBias)&coordMask)<<25 |
		(uint64(r+coordBias) & coordMask))
}

// Mode returns the 4-bit mode field (1 for cells).
func (c Cell) Mode() int { return int(uint64(c) >> 59) }

// Resolution returns the cell resolution in [0, MaxResolution].
func (c Cell) Resolution() int { return int(uint64(c)>>55) & 0xf }

// Face returns the icosahedron face index in [0, 20).
func (c Cell) Face() int { return int(uint64(c)>>50) & 0x1f }

// Q returns the axial q coordinate on the cell's face lattice.
func (c Cell) Q() int { return int(uint64(c)>>25)&coordMask - coordBias }

// R returns the axial r coordinate on the cell's face lattice.
func (c Cell) R() int { return int(uint64(c)&coordMask) - coordBias }

// axialToXY maps integer axial coordinates to face-local 2D coordinates
// for a pointy-top hexagon layout.
func axialToXY(q, r int, size float64) (x, y float64) {
	x = size * sqrt3 * (float64(q) + 0.5*float64(r))
	y = size * 1.5 * float64(r)
	return x, y
}

// xyToAxial maps face-local 2D coordinates to the nearest axial hexagon
// using cube-coordinate rounding.
func xyToAxial(x, y, size float64) (q, r int) {
	qf := (x/sqrt3 - y/3) / size
	rf := (2.0 / 3.0 * y) / size
	return cubeRound(qf, rf)
}

func cubeRound(qf, rf float64) (int, int) {
	sf := -qf - rf
	rq := math.Round(qf)
	rr := math.Round(rf)
	rs := math.Round(sf)
	dq := math.Abs(rq - qf)
	dr := math.Abs(rr - rf)
	ds := math.Abs(rs - sf)
	if dq > dr && dq > ds {
		rq = -rr - rs
	} else if dr <= ds {
		rr = -rq - rs
	}
	return int(rq), int(rr)
}

// centerVec returns the (unnormalized) 3D vector of the cell centroid on
// its face plane.
func (c Cell) centerVec() vec3 {
	f := &faces[c.Face()]
	x, y := axialToXY(c.Q(), c.R(), hexSize(c.Resolution()))
	return f.a.add(f.u.scale(x)).add(f.v.scale(y))
}

// Encode converts a latitude/longitude pair (degrees) to the canonical
// cell at the given resolution containing that point. It panics if res is
// out of range.
func Encode(lat, lng float64, res int) Cell {
	if res < 0 || res > MaxResolution {
		panic(ErrInvalidResolution)
	}
	s := latLngToVec(lat, lng)
	fi := faceContaining(s)
	f := &faces[fi]
	p := s.scale(f.aw / s.dot(f.w))
	d := p.sub(f.a)
	q, r := xyToAxial(d.dot(f.u), d.dot(f.v), hexSize(res))
	return resolveCell(fi, res, q, r)
}

// resolveCell maps axial coordinates (q, r) on faceIdx to the canonical
// cell owning that hexagon. If the hexagon center lies on another face's
// territory (across an edge, or exactly on a boundary owned by a
// lower-indexed face), the center ray is re-projected onto the owning face
// and rounded there. Because all face lattices unfold into one global
// lattice, the re-projected coordinates are exact. Only integer axial
// arithmetic is needed unless a face edge is actually crossed, so hot-path
// neighborhood traversal involves no trigonometry.
func resolveCell(faceIdx, res, q, r int) Cell {
	size := hexSize(res)
	// Canonicalize iteratively: snap to the lattice of whichever face
	// canonically owns the current center ray. Each pass either terminates
	// or moves the center onto the owning face's lattice; centers exactly
	// on a shared edge or vertex are lattice points of the global lattice
	// and converge immediately. The cap is only a safety net.
	for i := 0; i < 8; i++ {
		f := &faces[faceIdx]
		x, y := axialToXY(q, r, size)
		p := f.a.add(f.u.scale(x)).add(f.v.scale(y))
		s := p.normalized()
		nf := faceContaining(s)
		if nf == faceIdx {
			return makeCell(res, faceIdx, q, r)
		}
		nface := &faces[nf]
		pp := s.scale(nface.aw / s.dot(nface.w))
		d := pp.sub(nface.a)
		q, r = xyToAxial(d.dot(nface.u), d.dot(nface.v), size)
		faceIdx = nf
	}
	return makeCell(res, faceIdx, q, r)
}

// Center returns the latitude/longitude (degrees) of the cell centroid.
func (c Cell) Center() (lat, lng float64) {
	return vecToLatLng(c.centerVec().normalized())
}

// Valid reports whether c is a structurally sound, canonical cell: correct
// mode, resolution, face and coordinate ranges, and its hexagon center is
// canonically owned by its recorded face.
func (c Cell) Valid() bool {
	if c.Mode() != modeCell || c.Resolution() > MaxResolution || c.Face() >= numFaces {
		return false
	}
	q, r := c.Q(), c.R()
	if q < -coordBias || q >= coordBias || r < -coordBias || r >= coordBias {
		return false
	}
	return faceContaining(c.centerVec().normalized()) == c.Face()
}

// Parent returns the canonical cell at the given (coarser) resolution
// containing this cell's center.
func (c Cell) Parent(res int) (Cell, error) {
	if res < 0 || res > MaxResolution || res > c.Resolution() {
		return 0, ErrInvalidResolution
	}
	if res == c.Resolution() {
		return c, nil
	}
	lat, lng := c.Center()
	return Encode(lat, lng, res), nil
}

// Children returns the cells at the next finer resolution whose parent is
// c. Candidates are gathered by a 2-ring BFS around the child cell
// anchored at c's center, which geometrically covers every child
// (including across face edges, where gnomonic distortion makes fixed
// lattice windows unreliable), and is then filtered by exact parentage.
// The result is sorted for determinism; a cell has 4 children on average.
func (c Cell) Children() ([]Cell, error) {
	res := c.Resolution()
	if res >= MaxResolution {
		return nil, ErrInvalidResolution
	}
	lat, lng := c.Center()
	anchor := Encode(lat, lng, res+1)
	seen := make(map[Cell]struct{}, 8)
	var out []Cell
	for _, ch := range KRingDisk(anchor, 2) {
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		if p, err := ch.Parent(res); err == nil && p == c {
			out = append(out, ch)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// String returns the canonical 16-digit hexadecimal encoding.
func (c Cell) String() string {
	return fmt.Sprintf("%016x", uint64(c))
}

// Parse decodes a hexadecimal cell string produced by String.
func Parse(s string) (Cell, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, err
	}
	c := Cell(v)
	if !c.Valid() {
		return 0, ErrInvalidCell
	}
	return c, nil
}

// ApproxAreaKm2 returns the approximate cell area in square kilometers.
func (c Cell) ApproxAreaKm2() float64 {
	size := hexSize(c.Resolution())
	flat := 1.5 * sqrt3 * size * size
	d := c.centerVec().norm()
	r := earthRadiusKm / d
	return flat * r * r
}
