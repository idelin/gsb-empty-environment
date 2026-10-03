package hexgrid

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Cell is a 64-bit hierarchical hexagonal cell identifier.
//
// Bit layout (high to low):
//
//	63..60  mode (always 1 for a valid cell)
//	59..56  resolution (0..MaxRes)
//	55..51  icosahedron face (0..19)
//	50..0   grid index: i*(2^res) + j, needs 2*res bits
type Cell uint64

const (
	modeShift = 60
	resShift  = 56
	faceShift = 51
	indexMask = uint64(1)<<faceShift - 1
	cellMode  = uint64(1)
)

// ErrInvalidResolution is returned for resolutions outside [0, MaxRes].
var ErrInvalidResolution = errors.New("hexgrid: invalid resolution")

// ErrInvalidLatLng is returned for out-of-range coordinates.
var ErrInvalidLatLng = errors.New("hexgrid: invalid latitude/longitude")

// newCell packs (face, res, i, j) into a Cell. Callers must guarantee
// 0 <= face < numFaces, 0 <= res <= MaxRes and 0 <= i,j < 2^res.
func newCell(face, res, i, j int) Cell {
	idx := uint64(i)<<uint(res) | uint64(j)
	return Cell(cellMode<<modeShift | uint64(res)<<resShift | uint64(face)<<faceShift | idx)
}

// LatLngToCell encodes a WGS84 coordinate (degrees) into the cell at
// the given resolution that contains it.
func LatLngToCell(lat, lng float64, res int) (Cell, error) {
	if res < 0 || res > MaxRes {
		return 0, ErrInvalidResolution
	}
	if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, ErrInvalidLatLng
	}
	latRad := lat * math.Pi / 180
	lngRad := lng * math.Pi / 180
	p := [3]float64{
		math.Cos(latRad) * math.Cos(lngRad),
		math.Cos(latRad) * math.Sin(lngRad),
		math.Sin(latRad),
	}
	return cellAtSpherePoint(p, res), nil
}

// IsValid reports whether c is a well-formed cell identifier.
func (c Cell) IsValid() bool {
	if uint64(c)>>modeShift != cellMode {
		return false
	}
	res := c.Resolution()
	if res > MaxRes {
		return false
	}
	if c.Face() >= numFaces {
		return false
	}
	i, j := c.IJ()
	n := 1 << uint(res)
	return i >= 0 && i < n && j >= 0 && j < n
}

// Resolution returns the resolution of the cell (0..15 as stored).
func (c Cell) Resolution() int { return int(uint64(c)>>resShift) & 0xf }

// Face returns the icosahedron face index of the cell (0..19).
func (c Cell) Face() int { return int(uint64(c)>>faceShift) & 0x1f }

// IJ returns the grid coordinates of the cell on its face.
func (c Cell) IJ() (i, j int) {
	res := c.Resolution()
	idx := uint64(c) & indexMask
	return int(idx >> uint(res)), int(idx & (uint64(1)<<uint(res) - 1))
}

// Parent returns the cell's parent at resolution res-1. The second
// return value is false if the cell is already at resolution 0.
func (c Cell) Parent() (Cell, bool) {
	res := c.Resolution()
	if res == 0 {
		return 0, false
	}
	i, j := c.IJ()
	return newCell(c.Face(), res-1, i>>1, j>>1), true
}

// ParentTo returns the ancestor of the cell at the given (coarser or
// equal) resolution. It returns an error if res is out of range or
// finer than the cell's own resolution.
func (c Cell) ParentTo(res int) (Cell, error) {
	cur := c.Resolution()
	if res < 0 || res > MaxRes {
		return 0, ErrInvalidResolution
	}
	if res > cur {
		return 0, fmt.Errorf("hexgrid: cannot get parent at finer resolution %d of res-%d cell", res, cur)
	}
	i, j := c.IJ()
	shift := uint(cur - res)
	return newCell(c.Face(), res, i>>shift, j>>shift), nil
}

// Children returns the 4 direct children of the cell at res+1.
// The second return value is false at MaxRes, where no children exist.
func (c Cell) Children() ([4]Cell, bool) {
	res := c.Resolution()
	if res >= MaxRes {
		return [4]Cell{}, false
	}
	i, j := c.IJ()
	var out [4]Cell
	k := 0
	for di := 0; di < 2; di++ {
		for dj := 0; dj < 2; dj++ {
			out[k] = newCell(c.Face(), res+1, 2*i+di, 2*j+dj)
			k++
		}
	}
	return out, true
}

// String renders the cell as a hexadecimal string, e.g. "1188fb474e0dffff".
func (c Cell) String() string { return strconv.FormatUint(uint64(c), 16) }

// ParseCell decodes a hexadecimal cell string and validates it.
func ParseCell(s string) (Cell, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("hexgrid: cannot parse cell %q: %w", s, err)
	}
	c := Cell(v)
	if !c.IsValid() {
		return 0, fmt.Errorf("hexgrid: %q is not a valid cell", s)
	}
	return c, nil
}

// CenterLatLng returns the WGS84 coordinate (degrees) of the cell center.
func (c Cell) CenterLatLng() (lat, lng float64) {
	f, res := c.Face(), c.Resolution()
	i, j := c.IJ()
	p := cellCenterSphere(f, res, i, j)
	lat = math.Asin(p[2]) * 180 / math.Pi
	lng = math.Atan2(p[1], p[0]) * 180 / math.Pi
	return lat, lng
}

// Universe enumerates every structurally valid cell at the given
// resolution: numFaces * 4^res cells. It is intended for offline
// inspection; keep res modest (<= 12).
func Universe(res int) ([]Cell, error) {
	if res < 0 || res > MaxRes {
		return nil, ErrInvalidResolution
	}
	n := 1 << uint(res)
	out := make([]Cell, 0, numFaces*n*n)
	for f := 0; f < numFaces; f++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				out = append(out, newCell(f, res, i, j))
			}
		}
	}
	return out, nil
}

// IsReachable reports whether the cell can be produced by encoding
// some coordinate, i.e. whether its own center re-encodes to itself.
// Cells in the margin between the face triangle and the bounding
// grid square are structurally valid but unreachable.
func (c Cell) IsReachable() bool {
	f, res := c.Face(), c.Resolution()
	i, j := c.IJ()
	return cellAtSpherePoint(cellCenterSphere(f, res, i, j), res) == c
}
