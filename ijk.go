package hexgrid

// ijk is a hexagon grid coordinate in the IJK system: three integer axes at
// 120 degrees, kept normalized so that min(i, j, k) == 0. All operations in
// this file are pure integer arithmetic (except the float<->grid quantizers,
// which use only add/mul, never trig).
type ijk struct {
	i, j, k int
}

// unitVecs are the IJK offsets of the center cell (digit 0) and its six
// neighbors (digits 1..6): K, J, JK, I, IK, IJ.
var unitVecs = [7]ijk{
	{0, 0, 0},
	{0, 0, 1},
	{0, 1, 0},
	{0, 1, 1},
	{1, 0, 0},
	{1, 0, 1},
	{1, 1, 0},
}

// ringWalkDirs are the six side-walk directions used by the k-ring spiral,
// in order: +J, -I, +K, -J, +I, -K.
var ringWalkDirs = [6]ijk{
	{0, 1, 0},
	{-1, 0, 0},
	{0, 0, 1},
	{0, -1, 0},
	{1, 0, 0},
	{0, 0, -1},
}

func normalizeIJK(c ijk) ijk {
	if c.i < 0 {
		c.j -= c.i
		c.k -= c.i
		c.i = 0
	}
	if c.j < 0 {
		c.i -= c.j
		c.k -= c.j
		c.j = 0
	}
	if c.k < 0 {
		c.i -= c.k
		c.j -= c.k
		c.k = 0
	}
	min := c.i
	if c.j < min {
		min = c.j
	}
	if c.k < min {
		min = c.k
	}
	if min > 0 {
		c.i -= min
		c.j -= min
		c.k -= min
	}
	return c
}

func ijkAdd(a, b ijk) ijk { return ijk{a.i + b.i, a.j + b.j, a.k + b.k} }

// ijkNeighbor returns the neighbor of c in the given digit direction (1..6).
func ijkNeighbor(c ijk, digit int) ijk {
	return normalizeIJK(ijkAdd(c, unitVecs[digit]))
}

// ijkDistance is the hex-grid distance between two ijk coordinates.
func ijkDistance(a, b ijk) int {
	d := normalizeIJK(ijk{a.i - b.i, a.j - b.j, a.k - b.k})
	m := d.i
	if d.j > m {
		m = d.j
	}
	if d.k > m {
		m = d.k
	}
	return m
}

const sin60 = 0.86602540378443864676 // math.Sin(math.Pi/3), precomputed

// hex2dToIJK quantizes a point in the 2D hex-grid plane to the nearest ijk
// cell coordinate using cube-coordinate rounding (nearest lattice point in
// the sheared basis). Arithmetic only, no trigonometry.
func hex2dToIJK(x, y float64) ijk {
	// Lattice basis: q along (1, 0), r along (-1/2, sin60).
	zf := y / sin60
	xf := x + 0.5*zf
	yf := -xf - zf
	// Round cube coordinates, fixing the component with the largest error
	// so that rx+ry+rz == 0.
	rx := roundHalf(xf)
	ry := roundHalf(yf)
	rz := roundHalf(zf)
	dx := absf(rx - xf)
	dy := absf(ry - yf)
	dz := absf(rz - zf)
	switch {
	case dx > dy && dx > dz:
		rx = -ry - rz
	case dy > dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return normalizeIJK(ijk{int(rx), int(rz), 0})
}

func roundHalf(v float64) float64 {
	if v < 0 {
		return -roundHalf(-v)
	}
	return float64(int(v + 0.5))
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// ijkToHex2d maps an ijk coordinate to its 2D hex-grid plane position.
func ijkToHex2d(c ijk) (x, y float64) {
	i := c.i - c.k
	j := c.j - c.k
	x = float64(i) - 0.5*float64(j)
	y = float64(j) * sin60
	return
}
