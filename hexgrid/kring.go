package hexgrid

// axialDirs are the six neighbor directions of the hexagonal lattice in
// axial (q, r) coordinates.
var axialDirs = [6][2]int{
	{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1},
}

// normalize brings an out-of-range lattice position back into [0, n)^2 by
// repeatedly crossing rhombus boundary edges. All arithmetic is integer:
// positions are tracked in half-cell units (2*q+1) over a 2n grid so no
// floating point is ever involved. At most a handful of crossings occur
// (corner crossings need two); the loop is bounded defensively.
func (g *Grid) normalize(rh, res, q, r int) (int, int, int) {
	n := 1 << uint(res)
	for i := 0; i < 8; i++ {
		if q >= 0 && q < n && r >= 0 && r < n {
			return rh, q, r
		}
		var edge int
		switch {
		case q < 0:
			edge = 3
		case q >= n:
			edge = 1
		case r < 0:
			edge = 0
		default:
			edge = 2
		}
		// t: along-edge parameter, d: distance beyond the edge,
		// both in half-cell units (range 0..2n).
		var t, d int
		switch edge {
		case 0:
			t, d = 2*q+1, -(2*r + 1)
		case 1:
			t, d = 2*r+1, 2*q+1-2*n
		case 2:
			t, d = 2*q+1, 2*r+1-2*n
		case 3:
			t, d = 2*r+1, -(2*q + 1)
		}
		tr := g.transitions[rh][edge]
		if tr.flip {
			t = 2*n - t
		}
		rh = tr.rhombus
		switch tr.edge {
		case 0: // arrive on b=0 edge, interior at +b
			q, r = (t-1)/2, (d-1)/2
		case 1: // arrive on a=1 edge, interior at -a
			q, r = (2*n-d-1)/2, (t-1)/2
		case 2: // arrive on b=1 edge, interior at -b
			q, r = (t-1)/2, (2*n-d-1)/2
		case 3: // arrive on a=0 edge, interior at +a
			q, r = (d-1)/2, (t-1)/2
		}
	}
	panic("hexgrid: normalize failed to converge")
}

// stepDir moves one cell in the given axial direction, crossing rhombus
// boundaries when necessary. Pure integer arithmetic.
func (g *Grid) stepDir(rh, res, q, r, dir int) (int, int, int) {
	return g.normalize(rh, res, q+axialDirs[dir][0], r+axialDirs[dir][1])
}

// Neighbors returns the 6 cells adjacent to c in clockwise lattice order.
func (g *Grid) Neighbors(c CellID) [6]CellID {
	res, rh := c.Resolution(), c.Rhombus()
	q, r := c.Q(), c.R()
	var out [6]CellID
	for d := 0; d < 6; d++ {
		nrh, nq, nr := g.stepDir(rh, res, q, r, d)
		out[d] = MakeCell(nrh, res, nq, nr)
	}
	return out
}

// Ring returns the 6k cells at topological distance exactly k from c,
// walking the ring cell by cell so rhombus crossings stay exact.
func (g *Grid) Ring(c CellID, k int) []CellID {
	if k < 0 {
		panic("hexgrid: negative ring radius")
	}
	if k == 0 {
		return []CellID{c}
	}
	res, rh := c.Resolution(), c.Rhombus()
	// Ring walk anchor: center + k * axialDirs[4].
	rh, q, r := g.normalize(rh, res, c.Q()-k, c.R()+k)
	out := make([]CellID, 0, 6*k)
	for side := 0; side < 6; side++ {
		for j := 0; j < k; j++ {
			out = append(out, MakeCell(rh, res, q, r))
			rh, q, r = g.stepDir(rh, res, q, r, side)
		}
	}
	return out
}

// KRing returns all cells within topological distance k of c, ordered by
// increasing distance (center first, then ring 1..k). The result contains
// exactly 1 + 3k(k+1) cells for cells not near an icosahedron vertex
// defect. Duplicates (possible only around the 12 vertex defects) are
// removed while preserving order.
func (g *Grid) KRing(c CellID, k int) []CellID {
	if k < 0 {
		panic("hexgrid: negative ring radius")
	}
	total := 1 + 3*k*(k+1)
	out := make([]CellID, 0, total)
	seen := make(map[CellID]struct{}, total)
	push := func(cell CellID) {
		if _, dup := seen[cell]; !dup {
			seen[cell] = struct{}{}
			out = append(out, cell)
		}
	}
	push(c)
	for radius := 1; radius <= k; radius++ {
		for _, cell := range g.Ring(c, radius) {
			push(cell)
		}
	}
	return out
}
