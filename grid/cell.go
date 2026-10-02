package grid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxRes is the finest supported resolution.
const MaxRes = 14

const (
	numFaces  = 20
	numDigits = 15 // 1 res-0 leading digit + 14 refinement digits

	modeCell = 1

	resShift  = 52
	faceShift = 45
	modeShift = 59
)

var (
	ErrInvalidResolution = errors.New("grid: invalid resolution")
	ErrInvalidCell       = errors.New("grid: invalid cell index")
	// ErrFaceCrossing is returned when a neighborhood traversal would leave
	// the res-0 coverage of the origin icosahedron face.
	ErrFaceCrossing = errors.New("grid: neighborhood crosses icosahedron face boundary")

	errBrokenHierarchy = errors.New("grid: internal hierarchy inconsistency")
)

// Cell is a 64-bit hierarchical hexagonal cell index.
//
// Bit layout (MSB first):
//
//	bits 63-59  mode (1 = cell)
//	bits 58-56  reserved, must be 0
//	bits 55-52  resolution (0-14)
//	bits 51-45  icosahedron face (0-19)
//	bits 44-0   15 x 3-bit digits; digit[lvl] at bits 3*(14-lvl)..+2,
//	            digit[0] is the res-0 leading offset, unused digits are 7
type Cell uint64

// newCell packs a cell index. digits beyond res are set to 7.
func newCell(face, res int, digits [numDigits]uint8) Cell {
	c := uint64(modeCell) << modeShift
	c |= uint64(res) << resShift
	c |= uint64(face) << faceShift
	for lvl := 0; lvl < numDigits; lvl++ {
		d := uint8(7)
		if lvl <= res {
			d = digits[lvl]
		}
		c |= uint64(d) << (3 * (numDigits - 1 - lvl))
	}
	return Cell(c)
}

// Mode returns the index mode (1 for cells).
func (c Cell) Mode() int { return int(uint64(c) >> modeShift) }

// Res returns the cell resolution.
func (c Cell) Res() int { return int((uint64(c) >> resShift) & 0xF) }

// Face returns the icosahedron face index (0-19).
func (c Cell) Face() int { return int((uint64(c) >> faceShift) & 0x7F) }

// Digit returns the hierarchy digit at the given level (0..14).
func (c Cell) Digit(lvl int) int {
	return int((uint64(c) >> (3 * (numDigits - 1 - lvl))) & 7)
}

func (c Cell) digits() [numDigits]uint8 {
	var d [numDigits]uint8
	for lvl := 0; lvl < numDigits; lvl++ {
		d[lvl] = uint8(c.Digit(lvl))
	}
	return d
}

// IsValid reports whether the index is a well-formed cell.
func (c Cell) IsValid() bool {
	if c.Mode() != modeCell {
		return false
	}
	if (uint64(c)>>56)&0x7 != 0 {
		return false
	}
	if c.Res() > MaxRes {
		return false
	}
	if c.Face() >= numFaces {
		return false
	}
	for lvl := 0; lvl < numDigits; lvl++ {
		d := c.Digit(lvl)
		if lvl <= c.Res() {
			if d > 6 {
				return false
			}
		} else if d != 7 {
			return false
		}
	}
	return true
}

// IJ decodes the cell to its face-plane lattice coordinate at its
// resolution, using pure integer arithmetic.
func (c Cell) IJ() IJ {
	return digitsToIJ(c.digits(), c.Res())
}

// String renders the cell as a hexadecimal string.
func (c Cell) String() string {
	return strconv.FormatUint(uint64(c), 16)
}

// ParseCell parses a hexadecimal cell string produced by String.
func ParseCell(s string) (Cell, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return 0, fmt.Errorf("%w: empty string", ErrInvalidCell)
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidCell, err)
	}
	c := Cell(v)
	if !c.IsValid() {
		return 0, fmt.Errorf("%w: malformed bit pattern", ErrInvalidCell)
	}
	return c, nil
}

func validRes(res int) bool { return res >= 0 && res <= MaxRes }

// LatLngToCell encodes a geographic coordinate as a cell at the given
// resolution.
//
// The point is always quantized at MaxRes and the hierarchy digits are then
// truncated to the requested resolution. This makes the parent/child
// relation exact by construction: Parent(LatLngToCell(p, r), q) ==
// LatLngToCell(p, q) for every q <= r, with no boundary tie-break drift
// between resolutions.
func LatLngToCell(ll LatLng, res int) (Cell, error) {
	if !validRes(res) {
		return 0, ErrInvalidResolution
	}
	lat := ll.Lat
	if lat > 90 {
		lat = 90
	}
	if lat < -90 {
		lat = -90
	}
	lng := ll.Lng
	for lng > 180 {
		lng -= 360
	}
	for lng < -180 {
		lng += 360
	}
	p := latLngToVec3(LatLng{Lat: lat, Lng: lng})
	f := faceOf(p)
	x, y := project(f, p)
	ij := quantize(x, y, MaxRes)
	digits, err := ijToDigits(ij, MaxRes)
	if err != nil {
		return 0, err
	}
	return newCell(f, res, digits), nil
}

// cellFromIJ builds a cell from a face-plane lattice coordinate.
func cellFromIJ(face, res int, ij IJ) (Cell, error) {
	digits, err := ijToDigits(ij, res)
	if err != nil {
		return 0, err
	}
	return newCell(face, res, digits), nil
}
