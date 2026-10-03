package hexgrid

import (
	"errors"
	"fmt"
	"strconv"
)

// MaxResolution is the finest supported grid resolution.
const MaxResolution = 15

// Cell is a 64-bit hierarchical hexagonal grid index.
//
// Bit layout (H3-style):
//
//	[63:60] mode (4 bits, 1 = cell)
//	[59:56] reserved (4 bits, 0)
//	[55:52] resolution (4 bits, 0..15)
//	[51:45] base cell = icosahedron face (7 bits, 0..19)
//	[44:0]  15 hierarchy digits x 3 bits (digit i at bits 3*(15-i)..+2)
//
// Digits 0..6 address the 7 children of a cell (0 = centered child,
// 1..6 = the 6 neighbor directions). Unused trailing digits are 7.
type Cell uint64

const (
	cellMode      = 1
	reservedDigit = 7
)

var (
	// ErrInvalidCell is returned when a Cell fails structural validation.
	ErrInvalidCell = errors.New("hexgrid: invalid cell")
	// ErrInvalidResolution is returned for resolutions outside [0, 15].
	ErrInvalidResolution = errors.New("hexgrid: invalid resolution")
)

// makeCell assembles a Cell from its parts. digits must use sentinel 7
// for levels above res.
func makeCell(baseCell, res int, digits [MaxResolution]int) Cell {
	id := uint64(cellMode) << 60
	id |= uint64(res&0xf) << 52
	id |= uint64(baseCell&0x7f) << 45
	for i := 0; i < MaxResolution; i++ {
		id |= uint64(digits[i]&0x7) << (3 * (MaxResolution - 1 - i))
	}
	return Cell(id)
}

// Mode returns the 4-bit mode field (1 for cells).
func (c Cell) Mode() int { return int(uint64(c) >> 60) }

// Resolution returns the grid resolution of the cell (0..15).
func (c Cell) Resolution() int { return int(uint64(c)>>52) & 0xf }

// BaseCell returns the icosahedron face index (0..19) the cell belongs to.
func (c Cell) BaseCell() int { return int(uint64(c)>>45) & 0x7f }

// Digit returns the hierarchy digit at level i (1-based, 1..15).
func (c Cell) Digit(i int) int {
	if i < 1 || i > MaxResolution {
		return -1
	}
	return int(uint64(c)>>uint(3*(MaxResolution-i))) & 0x7
}

func (c Cell) digits() [MaxResolution]int {
	var d [MaxResolution]int
	for i := 1; i <= MaxResolution; i++ {
		d[i-1] = c.Digit(i)
	}
	return d
}

// IsValid reports whether the cell is structurally valid.
func (c Cell) IsValid() bool {
	if c.Mode() != cellMode {
		return false
	}
	if uint64(c)>>56&0xf != 0 { // reserved bits
		return false
	}
	if c.BaseCell() >= numFaces {
		return false
	}
	res := c.Resolution()
	for i := 1; i <= MaxResolution; i++ {
		d := c.Digit(i)
		if i <= res {
			if d > 6 {
				return false
			}
		} else if d != reservedDigit {
			return false
		}
	}
	return true
}

// String renders the cell as a hexadecimal string (H3-style).
func (c Cell) String() string {
	return strconv.FormatUint(uint64(c), 16)
}

// ParseCell parses a hexadecimal cell string produced by String.
func ParseCell(s string) (Cell, error) {
	id, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("hexgrid: parse cell %q: %w", s, err)
	}
	c := Cell(id)
	if !c.IsValid() {
		return 0, fmt.Errorf("hexgrid: parse cell %q: %w", s, ErrInvalidCell)
	}
	return c, nil
}

// Parent returns the cell's ancestor at the given (coarser) resolution.
// If res >= the cell's resolution the cell itself is returned.
func (c Cell) Parent(res int) Cell {
	if res < 0 {
		res = 0
	}
	if res >= c.Resolution() {
		return c
	}
	d := c.digits()
	for i := res; i < MaxResolution; i++ {
		d[i] = reservedDigit
	}
	return makeCell(c.BaseCell(), res, d)
}

// Child returns the child cell at resolution res+1 addressed by digit
// (0..6). It returns an error for invalid digits or at max resolution.
func (c Cell) Child(digit int) (Cell, error) {
	res := c.Resolution()
	if res >= MaxResolution {
		return 0, ErrInvalidResolution
	}
	if digit < 0 || digit > 6 {
		return 0, fmt.Errorf("hexgrid: invalid child digit %d", digit)
	}
	d := c.digits()
	d[res] = digit
	return makeCell(c.BaseCell(), res+1, d), nil
}

// Children returns all 7 children of the cell at the next finer resolution.
func (c Cell) Children() ([]Cell, error) {
	if c.Resolution() >= MaxResolution {
		return nil, ErrInvalidResolution
	}
	children := make([]Cell, 0, 7)
	for digit := 0; digit <= 6; digit++ {
		child, err := c.Child(digit)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, nil
}
