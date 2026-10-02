package hexgrid

import (
	"fmt"
	"strconv"
)

// CellID is a 64-bit hierarchical hexagon cell identifier.
//
// Bit layout (38 used bits):
//
//	[63:38] unused (zero)
//	[37:34] resolution (0..15)
//	[33:30] rhombus   (0..9)
//	[29:15] q axial coordinate (15 bits)
//	[14:0]  r axial coordinate (15 bits)
type CellID uint64

const (
	rShift       = 0
	qShift       = 15
	rhombusShift = 30
	resShift     = 34
	coordMask    = 1<<15 - 1
)

// MakeCell packs a (rhombus, resolution, q, r) tuple into a CellID.
// The caller must guarantee 0 <= q, r < 2^res and rhombus < NumRhombi.
func MakeCell(rhombus, res, q, r int) CellID {
	return CellID(uint64(res)<<resShift |
		uint64(rhombus)<<rhombusShift |
		uint64(q&coordMask)<<qShift |
		uint64(r&coordMask)<<rShift)
}

// Resolution returns the cell's hierarchical resolution (0..MaxResolution).
func (c CellID) Resolution() int { return int(uint64(c) >> resShift & 0xF) }

// Rhombus returns the icosahedron rhombus region (0..9) holding the cell.
func (c CellID) Rhombus() int { return int(uint64(c) >> rhombusShift & 0xF) }

// Q returns the axial q coordinate within the rhombus lattice.
func (c CellID) Q() int { return int(uint64(c) >> qShift & coordMask) }

// R returns the axial r coordinate within the rhombus lattice.
func (c CellID) R() int { return int(uint64(c) >> rShift & coordMask) }

// IsValid reports whether the cell's fields are internally consistent.
func (c CellID) IsValid() bool {
	res := c.Resolution()
	if res > MaxResolution || c.Rhombus() >= NumRhombi {
		return false
	}
	n := 1 << uint(res)
	return c.Q() < n && c.R() < n
}

// String renders the cell as a zero-padded 10-digit hexadecimal string.
func (c CellID) String() string { return fmt.Sprintf("%010x", uint64(c)) }

// ParseCellID decodes a hexadecimal cell string produced by String.
func ParseCellID(s string) (CellID, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("hexgrid: invalid cell %q: %v", s, err)
	}
	c := CellID(v)
	if !c.IsValid() {
		return 0, fmt.Errorf("hexgrid: cell %q fails validity checks", s)
	}
	return c, nil
}

// Parent returns the cell's parent at resolution res-1. Each cell has
// exactly 4 children, so the parent drops the least significant bit of
// both axial coordinates. It returns false at resolution 0.
func (c CellID) Parent() (CellID, bool) {
	res := c.Resolution()
	if res == 0 {
		return 0, false
	}
	return MakeCell(c.Rhombus(), res-1, c.Q()>>1, c.R()>>1), true
}

// ParentAt returns the ancestor of the cell at the given resolution.
// It returns false if target >= c.Resolution().
func (c CellID) ParentAt(target int) (CellID, bool) {
	res := c.Resolution()
	if target < 0 || target >= res {
		return 0, false
	}
	shift := uint(res - target)
	return MakeCell(c.Rhombus(), target, c.Q()>>shift, c.R()>>shift), true
}

// Children returns the 4 child cells at resolution res+1, or false if the
// cell is already at MaxResolution.
func (c CellID) Children() ([4]CellID, bool) {
	res := c.Resolution()
	if res >= MaxResolution {
		return [4]CellID{}, false
	}
	q, r := c.Q()*2, c.R()*2
	rh := c.Rhombus()
	return [4]CellID{
		MakeCell(rh, res+1, q, r),
		MakeCell(rh, res+1, q+1, r),
		MakeCell(rh, res+1, q, r+1),
		MakeCell(rh, res+1, q+1, r+1),
	}, true
}

// IsAncestorOf reports whether c is a strict ancestor of other.
func (c CellID) IsAncestorOf(other CellID) bool {
	res, ores := c.Resolution(), other.Resolution()
	if res >= ores || c.Rhombus() != other.Rhombus() {
		return false
	}
	shift := uint(ores - res)
	return c.Q() == other.Q()>>shift && c.R() == other.R()>>shift
}
