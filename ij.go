package hexgrid

import "errors"

// ErrOutsideBaseCell is returned when an IJ coordinate cannot be
// represented inside the base cell's digit hierarchy (the point lies
// beyond the region covered by the base cell at that resolution).
var ErrOutsideBaseCell = errors.New("hexgrid: coordinate outside base-cell hierarchy")

// IJ is an axial coordinate on a base cell's hexagonal lattice.
type IJ struct {
	I, J int
}

// digitOffsets maps digits 0..6 to child-center IJ offsets: digit 0 is
// the center child, digits 1..6 are the 6 unit ring children. The set
// forms a complete residue system modulo the aperture-7 map M, which is
// what makes the digit expansion a bijection over the whole lattice.
var digitOffsets = [7]IJ{
	{0, 0}, {1, 0}, {0, 1}, {-1, 0}, {0, -1}, {1, -1}, {-1, 1},
}

// residueToDigit maps (3*I + J) mod 7 to the unique digit whose offset
// leaves the point inside the image of M. Verified bijective by tests.
var residueToDigit = [7]uint8{0, 2, 5, 1, 3, 6, 4}

// ijDirections lists the 6 unit neighbor steps in cyclic (60 degree)
// order. Consecutive directions differ by a lattice 60-degree rotation,
// which the ring traversal relies on: D[i] + D[i+2] == D[i+1].
var ijDirections = [6]IJ{{1, 0}, {0, 1}, {-1, 1}, {-1, 0}, {0, -1}, {1, -1}}

// mulM applies the aperture-7 child transform M(i,j) = (2i-j, i+3j).
// det(M) = 7, so M scales areas by exactly 7 (one cell -> 7 children).
func mulM(p IJ) IJ { return IJ{2*p.I - p.J, p.I + 3*p.J} }

// divM applies M^-1; the caller guarantees the point lies in M's image
// (i.e. (3*I + J) mod 7 == 0 after offset removal), so division is exact.
func divM(p IJ) IJ { return IJ{(3*p.I + p.J) / 7, (2*p.J - p.I) / 7} }

func mod7(n int) int {
	m := n % 7
	if m < 0 {
		m += 7
	}
	return m
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// digitsToIJ folds a digit sequence into an IJ coordinate:
// IJ = sum_k M^(res-k) * offset(d_k). Pure integer arithmetic.
func digitsToIJ(digits []uint8) IJ {
	var p IJ
	for _, d := range digits {
		p = mulM(p)
		p.I += digitOffsets[d].I
		p.J += digitOffsets[d].J
	}
	return p
}

// ijToDigits expands an IJ coordinate into res digits by repeatedly
// peeling the unique digit that lands inside M's image. It fails when
// the point's ancestor chain does not terminate at the base-cell origin
// within res steps (point outside the base cell's covered region).
func ijToDigits(p IJ, res int) ([]uint8, bool) {
	digits := make([]uint8, res)
	for k := res - 1; k >= 0; k-- {
		d := residueToDigit[mod7(3*p.I+p.J)]
		digits[k] = d
		off := digitOffsets[d]
		p = divM(IJ{p.I - off.I, p.J - off.J})
	}
	if p.I != 0 || p.J != 0 {
		return nil, false
	}
	return digits, true
}

// ijDistance is the hex-grid (cube) distance between two IJ points.
func ijDistance(a, b IJ) int {
	di := a.I - b.I
	dj := a.J - b.J
	return (abs(di) + abs(dj) + abs(di+dj)) / 2
}

// ij returns the face-local IJ coordinate of a cell. Pure integer.
func (c Cell) ij() IJ { return digitsToIJ(c.Digits()) }

// cellFromIJ converts a face-local IJ coordinate back into a cell index.
func cellFromIJ(baseCell, res int, p IJ) (Cell, error) {
	digits, ok := ijToDigits(p, res)
	if !ok {
		return 0, ErrOutsideBaseCell
	}
	return NewCell(baseCell, res, digits)
}
