// Package hexgrid implements a discrete hexagonal hierarchical spatial
// index unfolded from an icosahedron, together with an offline formal
// constraint inspector and invariant assertion engine.
//
// Index bit layout (64-bit cell index):
//
//	bits 59..63  unused, must be 0
//	bits 56..58  mode, always 1 (cell)
//	bits 52..55  resolution 0..15
//	bits 47..51  base cell (icosahedron face) 0..19
//	bits 45..46  unused, must be 0
//	bits  0..44  15 digits x 3 bits; digit for resolution r sits at
//	             bit 3*(15-r); unused trailing digits are 7
//
// The hierarchy uses aperture 7: each cell has 7 children (a center
// child plus 6 ring children). Child IJ coordinates are obtained from
// the parent by the integer linear map M(i,j) = (2i-j, i+3j), which is
// multiplication by the hex-lattice algebraic integer 2+w of norm 7.
// Because {0} and the 6 unit offsets form a complete residue system
// modulo M, every integer IJ point belongs to exactly one digit
// expansion, so same-resolution cells form a complete hexagonal
// lattice and K-ring traversal is exact integer arithmetic with no
// floating-point or trigonometric operations.
package hexgrid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// MaxRes is the finest supported resolution.
	MaxRes = 15
	// NumBaseCells is the number of icosahedron faces (base cells).
	NumBaseCells = 20

	modeCell   = uint64(1)
	modeOffset = 56
	resOffset  = 52
	baseOffset = 47

	unusedDigit = uint8(7)
)

var (
	ErrInvalidResolution = errors.New("hexgrid: invalid resolution")
	ErrInvalidBaseCell   = errors.New("hexgrid: invalid base cell")
	ErrInvalidDigit      = errors.New("hexgrid: invalid digit")
	ErrInvalidCell       = errors.New("hexgrid: invalid cell index")
)

// Cell is a 64-bit hierarchical hexagonal cell index.
type Cell uint64

// digitShift returns the bit position of the digit for resolution r (1..15).
func digitShift(r int) uint { return uint(3 * (MaxRes - r)) }

// NewCell builds a cell index from its parts. len(digits) must equal res
// and every digit must be in [0,6].
func NewCell(baseCell, res int, digits []uint8) (Cell, error) {
	if res < 0 || res > MaxRes {
		return 0, ErrInvalidResolution
	}
	if baseCell < 0 || baseCell >= NumBaseCells {
		return 0, ErrInvalidBaseCell
	}
	if len(digits) != res {
		return 0, fmt.Errorf("hexgrid: resolution %d needs %d digits, got %d", res, res, len(digits))
	}
	c := Cell(modeCell<<modeOffset | uint64(res)<<resOffset | uint64(baseCell)<<baseOffset)
	for r := 1; r <= MaxRes; r++ {
		d := unusedDigit
		if r <= res {
			d = digits[r-1]
			if d > 6 {
				return 0, ErrInvalidDigit
			}
		}
		c |= Cell(d) << digitShift(r)
	}
	return c, nil
}

// MustCell is NewCell panicking on error; intended for tests and constants.
func MustCell(baseCell, res int, digits []uint8) Cell {
	c, err := NewCell(baseCell, res, digits)
	if err != nil {
		panic(err)
	}
	return c
}

// Mode returns the index mode field (always 1 for valid cells).
func (c Cell) Mode() int { return int((uint64(c) >> modeOffset) & 0x7) }

// Res returns the cell resolution in [0,15].
func (c Cell) Res() int { return int((uint64(c) >> resOffset) & 0xf) }

// BaseCell returns the icosahedron face id in [0,19].
func (c Cell) BaseCell() int { return int((uint64(c) >> baseOffset) & 0x1f) }

// Digit returns the digit at resolution r (1..15); 7 means unused.
func (c Cell) Digit(r int) uint8 { return uint8((uint64(c) >> digitShift(r)) & 0x7) }

// Digits returns the active digits (length == Res()).
func (c Cell) Digits() []uint8 {
	res := c.Res()
	d := make([]uint8, res)
	for r := 1; r <= res; r++ {
		d[r-1] = c.Digit(r)
	}
	return d
}

// IsValid reports whether all bit-level invariants of a cell index hold.
func (c Cell) IsValid() bool {
	u := uint64(c)
	if u>>59 != 0 { // top 5 bits must be zero
		return false
	}
	if (u>>modeOffset)&0x7 != modeCell {
		return false
	}
	if c.BaseCell() >= NumBaseCells {
		return false
	}
	if (u>>45)&0x3 != 0 { // spare bits between base cell and digits
		return false
	}
	res := c.Res()
	for r := 1; r <= MaxRes; r++ {
		d := c.Digit(r)
		if r <= res {
			if d > 6 {
				return false
			}
		} else if d != unusedDigit {
			return false
		}
	}
	return true
}

// Parent returns the cell at the next coarser resolution.
func (c Cell) Parent() (Cell, error) {
	res := c.Res()
	if res == 0 {
		return 0, errors.New("hexgrid: resolution 0 cell has no parent")
	}
	u := uint64(c)
	u &^= 0x7 << digitShift(res)
	u |= uint64(unusedDigit) << digitShift(res)
	u &^= 0xf << resOffset
	u |= uint64(res-1) << resOffset
	return Cell(u), nil
}

// Child returns the resolution res+1 child with the given digit (0..6).
func (c Cell) Child(d uint8) (Cell, error) {
	res := c.Res()
	if res == MaxRes {
		return 0, ErrInvalidResolution
	}
	if d > 6 {
		return 0, ErrInvalidDigit
	}
	u := uint64(c)
	u &^= 0x7 << digitShift(res+1)
	u |= uint64(d) << digitShift(res+1)
	u &^= 0xf << resOffset
	u |= uint64(res+1) << resOffset
	return Cell(u), nil
}

// Children returns the 7 children at resolution res+1 (digit order 0..6).
func (c Cell) Children() ([]Cell, error) {
	if c.Res() == MaxRes {
		return nil, ErrInvalidResolution
	}
	out := make([]Cell, 7)
	for d := 0; d < 7; d++ {
		out[d], _ = c.Child(uint8(d))
	}
	return out, nil
}

// String renders the cell as a hexadecimal string (acceptance format).
func (c Cell) String() string { return strconv.FormatUint(uint64(c), 16) }

// ParseCell parses a hexadecimal cell string, validating all invariants.
func ParseCell(s string) (Cell, error) {
	t := strings.TrimSpace(strings.ToLower(s))
	t = strings.TrimPrefix(t, "0x")
	if t == "" {
		return 0, ErrInvalidCell
	}
	u, err := strconv.ParseUint(t, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("hexgrid: cannot parse %q: %v", s, err)
	}
	c := Cell(u)
	if !c.IsValid() {
		return 0, fmt.Errorf("%w: %q", ErrInvalidCell, s)
	}
	return c, nil
}
