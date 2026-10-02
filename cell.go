package hexgrid

import (
	"errors"
	"strconv"
)

// Cell is a 64-bit hierarchical hexagonal cell index.
//
// Bit layout (H3-style):
//
//	63..60  mode (1 = cell)
//	59..56  resolution (0..15)
//	55..48  base cell (face*7 + root hexagon, 0..139)
//	47..3   15 digits x 3 bits (digit for level i, 0..6; 7 = unused)
//	2..0    reserved
const (
	maxRes      = 15
	numDigits   = 15
	modeCell    = uint64(1)
	modeShift   = 60
	resShift    = 56
	baseShift   = 48
	digitMask   = uint64(7)
	unusedDigit = 7
)

// Cell is a hierarchical hexagonal cell index.
type Cell uint64

func digitShift(level int) uint { return uint(3*(numDigits-level) + 3) }

// NewCell builds a cell index from its parts. digits[i] is the digit for
// level i+1; entries beyond res are ignored (stored as 7).
func NewCell(face, root, res int, digits [numDigits]uint8) Cell {
	v := modeCell<<modeShift |
		uint64(res&0xF)<<resShift |
		uint64(face*rootHexesPerFace+root)<<baseShift
	for i := 0; i < numDigits; i++ {
		d := uint64(unusedDigit)
		if i < res {
			d = uint64(digits[i] & 7)
		}
		v |= d << digitShift(i+1)
	}
	return Cell(v)
}

// Mode returns the index mode (1 for cells).
func (c Cell) Mode() int { return int(uint64(c) >> modeShift) }

// Resolution returns the cell resolution, 0..15.
func (c Cell) Resolution() int { return int((uint64(c) >> resShift) & 0xF) }

// BaseCell returns the base cell number, 0..139.
func (c Cell) BaseCell() int { return int((uint64(c) >> baseShift) & 0xFF) }

// Face returns the icosahedron face, 0..19.
func (c Cell) Face() int { return c.BaseCell() / rootHexesPerFace }

// Digit returns the digit (0..6) at the given 1-based level, or 7 if unused.
func (c Cell) Digit(level int) int {
	return int((uint64(c) >> digitShift(level)) & digitMask)
}

// IsValid reports whether c is a well-formed cell index.
func (c Cell) IsValid() bool {
	if c.Mode() != int(modeCell) {
		return false
	}
	res := c.Resolution()
	if res > maxRes || c.BaseCell() >= numBaseCells {
		return false
	}
	for i := 1; i <= numDigits; i++ {
		d := c.Digit(i)
		if i <= res {
			if d > 6 {
				return false
			}
		} else if d != unusedDigit {
			return false
		}
	}
	return true
}

// String returns the hexadecimal representation of the index, e.g. "881f1c4d7ffffff".
func (c Cell) String() string { return strconv.FormatUint(uint64(c), 16) }

// ParseCell parses a hexadecimal cell string produced by String.
func ParseCell(s string) (Cell, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, err
	}
	c := Cell(v)
	if !c.IsValid() {
		return 0, errors.New("hexgrid: invalid cell index")
	}
	return c, nil
}

// Parent returns the cell truncated to the given (coarser) resolution.
// Asking for a resolution >= the cell's own returns the cell unchanged.
func (c Cell) Parent(res int) Cell {
	cur := c.Resolution()
	if res < 0 {
		res = 0
	}
	if res >= cur {
		return c
	}
	v := uint64(c)
	v = (v &^ (0xF << resShift)) | uint64(res)<<resShift
	for i := res + 1; i <= cur; i++ {
		sh := digitShift(i)
		v = (v &^ (digitMask << sh)) | (uint64(unusedDigit) << sh)
	}
	return Cell(v)
}

// Child returns the child cell at the next finer resolution selected by
// digit (0 = center child, 1..6 = surrounding children).
func (c Cell) Child(digit int) Cell {
	res := c.Resolution()
	if res >= maxRes || digit < 0 || digit > 6 {
		return c
	}
	v := uint64(c)
	v = (v &^ (0xF << resShift)) | uint64(res+1)<<resShift
	sh := digitShift(res + 1)
	v = (v &^ (digitMask << sh)) | uint64(digit)<<sh
	return Cell(v)
}

// Children returns the 7 children of the cell at the next finer resolution.
func (c Cell) Children() [7]Cell {
	var out [7]Cell
	for d := 0; d < 7; d++ {
		out[d] = c.Child(d)
	}
	return out
}

// digits extracts the per-level digits of the cell.
func (c Cell) digits() (digits [numDigits]uint8) {
	res := c.Resolution()
	for i := 1; i <= res; i++ {
		digits[i-1] = uint8(c.Digit(i))
	}
	return digits
}
