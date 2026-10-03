package hexgrid

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrInvalidResolution is returned for resolutions outside [0, MaxRes].
var ErrInvalidResolution = errors.New("hexgrid: invalid resolution")

// ErrInvalidCell is returned for malformed cell identifiers.
var ErrInvalidCell = errors.New("hexgrid: invalid cell")

var errNormalization = errors.New("hexgrid: point normalization failed")

// Cell is a 64-bit hierarchical hexagonal cell identifier.
//
// Bit layout (H3-inspired):
//
//	63      unused (0)
//	62..59  mode (1 = cell)
//	58..56  reserved (0)
//	55..52  resolution (0..15)
//	51..47  canonical icosahedron face (0..19)
//	46..45  base vertex slot of the face (0..2)
//	44..0   15 digits of 3 bits, digit l at bits 3*(15-l); unused = 7
type Cell uint64

const (
	modeCell   = 1
	modeOffset = 59
	resOffset  = 52
	faceOffset = 47
	slotOffset = 45
)

// slotVecs maps a base vertex slot to its lattice coordinate at
// frequency 1: slot s is the face vertex icosaFaces[face][s].
var slotVecs = [3]vec2{{0, 0}, {1, 0}, {0, 1}}

// Mode returns the identifier mode (always 1 for cells).
func (c Cell) Mode() int { return int(uint64(c) >> modeOffset & 0xF) }

// Resolution returns the cell resolution, 0..MaxRes.
func (c Cell) Resolution() int { return int(uint64(c) >> resOffset & 0xF) }

// Face returns the canonical icosahedron face of the cell, 0..19.
func (c Cell) Face() int { return int(uint64(c) >> faceOffset & 0x1F) }

// String returns the canonical hexadecimal representation of the cell.
func (c Cell) String() string { return strconv.FormatUint(uint64(c), 16) }

func digitShift(res int) uint { return uint(3 * (MaxRes - res)) }

func getDigit(c Cell, res int) int {
	return int(uint64(c) >> digitShift(res) & 7)
}

func setDigit(c Cell, res, d int) Cell {
	return Cell(uint64(c)&^(uint64(7)<<digitShift(res)) | uint64(d)<<digitShift(res))
}

// coord is the decoded internal form of a cell: canonical face plus
// cell coordinate c, where the cell center on the face is the lattice
// point (2+w)^res * c at frequency 7^res.
type coord struct {
	face int
	c    vec2
	res  int
}

// decodeCell parses a cell identifier into its internal coordinate.
func decodeCell(cell Cell) (coord, error) {
	if cell.Mode() != modeCell {
		return coord{}, ErrInvalidCell
	}
	res := cell.Resolution()
	face := cell.Face()
	slot := int(uint64(cell) >> slotOffset & 0x3)
	if res > MaxRes || face >= 20 || slot > 2 {
		return coord{}, ErrInvalidCell
	}
	c := slotVecs[slot]
	for l := 1; l <= res; l++ {
		d := getDigit(cell, l)
		if d > 6 {
			return coord{}, ErrInvalidCell
		}
		c = childCoord(c, d)
	}
	for l := res + 1; l <= MaxRes; l++ {
		if getDigit(cell, l) != 7 {
			return coord{}, ErrInvalidCell
		}
	}
	return coord{face: face, c: c, res: res}, nil
}

// encodeCoord builds the cell identifier for a canonical face and cell
// coordinate. Pure integer arithmetic.
func encodeCoord(face int, c vec2, res int) (Cell, error) {
	if res < 0 || res > MaxRes || face < 0 || face >= 20 {
		return 0, ErrInvalidCell
	}
	var digits [MaxRes]int
	cur := c
	for l := res; l >= 1; l-- {
		p := parentCoord(cur)
		d, ok := digitOf(cur.sub(mulMc(p)))
		if !ok {
			return 0, ErrInvalidCell
		}
		digits[l-1] = d
		cur = p
	}
	slot := -1
	for s, v := range slotVecs {
		if v == cur {
			slot = s
		}
	}
	if slot < 0 {
		return 0, ErrInvalidCell
	}
	id := uint64(modeCell)<<modeOffset |
		uint64(res)<<resOffset |
		uint64(face)<<faceOffset |
		uint64(slot)<<slotOffset
	cell := Cell(id)
	for l := 1; l <= MaxRes; l++ {
		d := 7
		if l <= res {
			d = digits[l-1]
		}
		cell = setDigit(cell, l, d)
	}
	return cell, nil
}

// cellToPoint decodes a cell to its canonical face and integer
// barycentric center at frequency 7^res.
func cellToPoint(cell Cell) (int, [3]int64, error) {
	cc, err := decodeCell(cell)
	if err != nil {
		return 0, [3]int64{}, err
	}
	p := powM(cc.res, cc.c)
	return cc.face, baryFromIJ(p, freqTable[cc.res]), nil
}

// cellFromCoord canonicalizes a (face, cell coordinate) pair and
// encodes it as a cell identifier.
func cellFromCoord(face int, c vec2, res int) (Cell, error) {
	p := powM(res, c)
	w := baryFromIJ(p, freqTable[res])
	f, w, err := normalize(face, w)
	if err != nil {
		return 0, err
	}
	f, w = canonicalize(f, w)
	return encodeCoord(f, divMPow(res, ijFromBary(w)), res)
}

// ParseCell parses a hexadecimal cell string and validates that it is
// well formed and canonical.
func ParseCell(s string) (Cell, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidCell, err)
	}
	cell := Cell(v)
	face, w, err := cellToPoint(cell)
	if err != nil {
		return 0, err
	}
	for _, x := range w {
		if x < 0 {
			return 0, fmt.Errorf("%w: center outside face", ErrInvalidCell)
		}
	}
	for m := 0; m < 3; m++ {
		if w[m] == 0 && faceCross[face][m].face < face {
			return 0, fmt.Errorf("%w: non-canonical face", ErrInvalidCell)
		}
	}
	return cell, nil
}

// IsPentagon reports whether the cell is one of the 12 pentagon cells
// centered on the icosahedron vertices.
func (c Cell) IsPentagon() bool {
	_, w, err := cellToPoint(c)
	if err != nil {
		return false
	}
	zeros := 0
	for _, x := range w {
		if x == 0 {
			zeros++
		}
	}
	return zeros == 2
}

// BaseCells returns the 12 resolution-0 cells, one per icosahedron
// vertex. All of them are pentagons.
func BaseCells() []Cell {
	var best [12]int
	for v := range best {
		best[v] = -1
	}
	for f, face := range icosaFaces {
		for _, v := range face {
			if best[v] < 0 || f < best[v] {
				best[v] = f
			}
		}
	}
	cells := make([]Cell, 0, 12)
	for v := 0; v < 12; v++ {
		f := best[v]
		slot := -1
		for s, vv := range icosaFaces[f] {
			if vv == v {
				slot = s
			}
		}
		cell, err := encodeCoord(f, slotVecs[slot], 0)
		if err != nil {
			panic(err)
		}
		cells = append(cells, cell)
	}
	return cells
}
