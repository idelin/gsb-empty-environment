package hexgrid

// Topological K-ring neighbourhood traversal.
//
// Same-face traversal is pure integer arithmetic on axial coordinates: no
// floating point, no trigonometry, no allocations (results are appended to
// a caller-supplied buffer). Cells whose neighbourhood crosses an
// icosahedron face edge are re-anchored through the gnomonic projection
// (dot products + one sqrt, still no trig).

// GridDisk appends to dst all cells within hexagonal distance k of c
// (including c itself), sorted and deduplicated. Count is 1 + 3k(k+1) for
// cells away from face edges. Returns dst extended.
func GridDisk(c Cell, k int, dst []Cell) []Cell {
	if k < 0 {
		return dst
	}
	face, q, r, res := axialOf(c)
	start := len(dst)
	for dq := -k; dq <= k; dq++ {
		lo := -k
		if -dq-k > lo {
			lo = -dq - k
		}
		hi := k
		if -dq+k < hi {
			hi = -dq + k
		}
		for dr := lo; dr <= hi; dr++ {
			dst = append(dst, cellFromAxial(face, q+int64(dq), r+int64(dr), res))
		}
	}
	sub := dst[start:]
	sortCells(sub)
	return dst[:start+uniqueCells(sub)]
}

// GridRing appends to dst the cells at exact hexagonal distance k from c
// (the hollow ring), sorted and deduplicated. Count is 6k for k >= 1.
func GridRing(c Cell, k int, dst []Cell) []Cell {
	if k < 0 {
		return dst
	}
	if k == 0 {
		return append(dst, c)
	}
	face, q, r, res := axialOf(c)
	start := len(dst)
	// Start at k steps along direction 4, then walk the 6 sides.
	cq := q + int64(k)*ringDirs[4][0]
	cr := r + int64(k)*ringDirs[4][1]
	for side := 0; side < 6; side++ {
		for step := 0; step < k; step++ {
			dst = append(dst, cellFromAxial(face, cq, cr, res))
			cq += ringDirs[side][0]
			cr += ringDirs[side][1]
		}
	}
	sub := dst[start:]
	sortCells(sub)
	return dst[:start+uniqueCells(sub)]
}

// Neighbors appends the up-to-6 cells adjacent to c.
func Neighbors(c Cell, dst []Cell) []Cell { return GridRing(c, 1, dst) }

// HexDistance returns the topological (hexagonal grid) distance between two
// cells. ok is false when the cells differ in resolution or lie on different
// icosahedron faces (cross-face distance is not computed by this lite
// implementation).
func HexDistance(a, b Cell) (dist int, ok bool) {
	fa, qa, ra, resA := axialOf(a)
	fb, qb, rb, resB := axialOf(b)
	if fa != fb || resA != resB {
		return 0, false
	}
	dq := qa - qb
	dr := ra - rb
	return int((abs64(dq) + abs64(dr) + abs64(dq+dr)) / 2), true
}

// cellFromAxial builds the cell at absolute axial coordinates (q, r) on the
// given face. Coordinates outside the face's 7 root hexagons are re-anchored
// onto the correct neighbouring face via the gnomonic projection.
func cellFromAxial(face int, q, r int64, res int) Cell {
	root, _, _, digits := reduceAxial(q, r, res)
	if root >= 0 {
		return NewCell(face, root, res, digits)
	}
	x, y := axialToCartBase(q, r, res)
	p := unprojectFace(face, x, y)
	return cellFromVec(p, res)
}

// sortCells insertion-sorts a small slice of cells in place (no reflection,
// no allocation).
func sortCells(s []Cell) {
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i - 1
		for j >= 0 && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}

// uniqueCells compacts a sorted slice in place and returns the new length.
func uniqueCells(s []Cell) int {
	if len(s) == 0 {
		return 0
	}
	n := 1
	for i := 1; i < len(s); i++ {
		if s[i] != s[n-1] {
			s[n] = s[i]
			n++
		}
	}
	return n
}
