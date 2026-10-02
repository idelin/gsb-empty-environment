package grid

// IJ is a 2D hexagonal lattice coordinate on an icosahedron face plane.
// The I and J axes meet at 60 degrees; the implicit third cube coordinate
// is K = -I - J. All K-ring traversal arithmetic happens in this integer
// space with no floating point involved.
type IJ struct {
	I, J int
}

func (a IJ) Add(b IJ) IJ { return IJ{a.I + b.I, a.J + b.J} }
func (a IJ) Sub(b IJ) IJ { return IJ{a.I - b.I, a.J - b.J} }

func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Dist returns the hex-grid (ring) distance between two lattice points.
func (a IJ) Dist(b IJ) int {
	di := iabs(a.I - b.I)
	dj := iabs(a.J - b.J)
	dk := iabs((a.I - b.I) + (a.J - b.J))
	m := di
	if dj > m {
		m = dj
	}
	if dk > m {
		m = dk
	}
	return m
}

// digitOffsets maps a direction digit (0-6) to a lattice offset. Digit 0 is
// the center; digits 1-6 are the six unit neighbors.
var digitOffsets = [7]IJ{
	{0, 0},
	{1, 0}, {0, 1}, {-1, 1},
	{-1, 0}, {0, -1}, {1, -1},
}

// digitOf returns the direction digit for a lattice offset, or -1 if the
// offset is not the center or an immediate neighbor.
func digitOf(d IJ) int {
	switch d {
	case IJ{0, 0}:
		return 0
	case IJ{1, 0}:
		return 1
	case IJ{0, 1}:
		return 2
	case IJ{-1, 1}:
		return 3
	case IJ{-1, 0}:
		return 4
	case IJ{0, -1}:
		return 5
	case IJ{1, -1}:
		return 6
	}
	return -1
}

// Aperture-7 hierarchy transforms, pure integer matrix ops with determinant
// 7. downAp7 maps a coarse lattice point into the next finer lattice
// (rotation ~+19.1 degrees, used at even levels); downAp7r is the mirrored
// variant (rotation ~+40.9 degrees, used at odd levels). The two compose to
// a 60 degree rotation so the lattice realigns every two resolutions.

func downAp7(p IJ) IJ  { return IJ{2*p.I - p.J, p.I + 3*p.J} }
func downAp7r(p IJ) IJ { return IJ{p.I - 2*p.J, 2*p.I + 3*p.J} }

// upAp7 / upAp7r map a fine lattice point to the nearest coarse lattice
// point (the parent cell center), using integer cube rounding.

func upAp7(p IJ) IJ  { return latticeRound(3*p.I+p.J, -p.I+2*p.J) }
func upAp7r(p IJ) IJ { return latticeRound(3*p.I+2*p.J, -2*p.I+p.J) }

func downForLevel(level int, p IJ) IJ {
	if level%2 == 1 {
		return downAp7r(p)
	}
	return downAp7(p)
}

func upForLevel(level int, p IJ) IJ {
	if level%2 == 1 {
		return upAp7r(p)
	}
	return upAp7(p)
}

// latticeRound rounds (a/7, b/7) to the nearest point of the triangular
// lattice using cube-coordinate rounding, entirely in integer arithmetic.
func latticeRound(a, b int) IJ {
	c := -a - b
	qa, ea := roundDiv7(a)
	qb, eb := roundDiv7(b)
	qc, ec := roundDiv7(c)
	// Force qa+qb+qc == 0 by recomputing the component with the largest
	// rounding error.
	switch {
	case ea >= eb && ea >= ec:
		qa = -qb - qc
	case eb >= ec:
		qb = -qa - qc
	}
	return IJ{qa, qb}
}

// roundDiv7 rounds n/7 to the nearest integer (ties cannot occur since
// n mod 7 is integral) and returns the absolute error numerator.
func roundDiv7(n int) (q, errNum int) {
	q = n / 7
	r := n % 7
	if iabs(r) >= 4 {
		if n > 0 {
			q++
		} else {
			q--
		}
	}
	return q, iabs(n - 7*q)
}

// ijToDigits extracts the res+1 hierarchy digits for a res-r lattice point.
// digits[0] is the res-0 leading offset (must be 0-6, i.e. the point must
// lie inside the face's res-0 coverage); digits[level] is the child digit at
// that level. Returns ErrFaceCrossing when the point lies outside the face's
// res-0 coverage.
func ijToDigits(p IJ, res int) ([numDigits]uint8, error) {
	var digits [numDigits]uint8
	for level := res; level >= 1; level-- {
		parent := upForLevel(level, p)
		child := downForLevel(level, parent)
		d := digitOf(p.Sub(child))
		if d < 0 {
			return digits, errBrokenHierarchy
		}
		digits[level] = uint8(d)
		p = parent
	}
	d0 := digitOf(p)
	if d0 < 0 {
		return digits, ErrFaceCrossing
	}
	digits[0] = uint8(d0)
	return digits, nil
}

// digitsToIJ composes hierarchy digits back into a res-r lattice point.
// Pure integer arithmetic; the exact inverse of ijToDigits.
func digitsToIJ(digits [numDigits]uint8, res int) IJ {
	p := digitOffsets[0].Add(digitOffsets[digits[0]])
	for level := 1; level <= res; level++ {
		p = downForLevel(level, p).Add(digitOffsets[digits[level]])
	}
	return p
}
