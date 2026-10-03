package hexgrid

import "math"

// cubeCoord is an integer cube (x+y+z=0) coordinate on a face-local
// hexagonal lattice. All topological traversal (K-Ring) is performed
// with pure integer arithmetic on these coordinates.
type cubeCoord struct {
	x, y, z int32
}

func (a cubeCoord) add(b cubeCoord) cubeCoord { return cubeCoord{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a cubeCoord) sub(b cubeCoord) cubeCoord { return cubeCoord{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a cubeCoord) double() cubeCoord         { return cubeCoord{2 * a.x, 2 * a.y, 2 * a.z} }

// cubeDirections are the 6 unit neighbor offsets in cube space.
var cubeDirections = [6]cubeCoord{
	{1, -1, 0}, {1, 0, -1}, {0, 1, -1},
	{-1, 1, 0}, {-1, 0, 1}, {0, -1, 1},
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// cubeDistance is the hex-grid graph distance between two cube coords.
func cubeDistance(a, b cubeCoord) int {
	d := a.sub(b)
	return int((abs32(d.x) + abs32(d.y) + abs32(d.z)) / 2)
}

// cubeRoundFloat rounds fractional cube coordinates to the nearest valid
// integer cube coordinate (preserving x+y+z=0).
func cubeRoundFloat(x, y, z float64) cubeCoord {
	rx, ry, rz := math.Round(x), math.Round(y), math.Round(z)
	dx, dy, dz := math.Abs(rx-x), math.Abs(ry-y), math.Abs(rz-z)
	switch {
	case dx > dy && dx > dz:
		rx = -ry - rz
	case dy > dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return cubeCoord{int32(rx), int32(ry), int32(rz)}
}

// baseHexSize is the hexagon circumradius (in gnomonic plane units, ~radians)
// at resolution 0. It is chosen so that a whole icosahedron face fits inside
// the single resolution-0 cell and every resolution-1 coordinate quantizes
// to the center or one of the 6 direct neighbors (a valid 7-ary hierarchy).
const baseHexSize = 1.2

// hexSize returns the hexagon circumradius at the given resolution.
// Each resolution level halves the lattice scale.
func hexSize(res int) float64 {
	return baseHexSize / math.Exp2(float64(res))
}

var sqrt3 = math.Sqrt(3)

// planeToCube converts face-plane coordinates to the nearest cube coord
// at the given resolution (pointy-top axial layout).
func planeToCube(px, py float64, res int) cubeCoord {
	s := hexSize(res)
	q := (sqrt3/3*px - 1.0/3*py) / s
	r := (2.0 / 3 * py) / s
	return cubeRoundFloat(q, -q-r, r)
}

// cubeToPlane converts a cube coord to face-plane coordinates of its
// hexagon center at the given resolution.
func cubeToPlane(c cubeCoord, res int) (px, py float64) {
	s := hexSize(res)
	q, r := float64(c.x), float64(c.z)
	return s * sqrt3 * (q + r/2), s * 1.5 * r
}

// digitDirection maps a hierarchy digit (0..6) to its cube offset:
// digit 0 is the centered child, digits 1..6 are the 6 neighbor directions.
func digitDirection(digit int) cubeCoord {
	if digit <= 0 || digit > 6 {
		return cubeCoord{}
	}
	return cubeDirections[digit-1]
}

// digitFromDiff maps a cube offset back to a hierarchy digit. The offset
// produced by the halving chain is always the zero vector or a unit
// direction; a nearest-direction fallback keeps the mapping total.
func digitFromDiff(diff cubeCoord) int {
	if diff == (cubeCoord{}) {
		return 0
	}
	best, bestDist := 1, 1<<30
	for i, dir := range cubeDirections {
		if d := cubeDistance(diff, dir); d < bestDist {
			best, bestDist = i+1, d
		}
	}
	return best
}

// digitsFromCube decomposes a cube coordinate at resolution res into the
// hierarchical digit chain (digits[0] is level 1). Levels above res are
// filled with the unused sentinel digit 7.
//
// Invariant: C_0 = (0,0,0) (a whole face is one cell at resolution 0) and
// C_i = round(C_{i+1} / 2), so each residual C_{i+1} - 2*C_i is always one
// of the 7 encodable child positions.
func digitsFromCube(c cubeCoord, res int) [MaxResolution]int {
	var digits [MaxResolution]int
	for i := range digits {
		digits[i] = 7
	}
	if res <= 0 {
		return digits
	}
	chain := make([]cubeCoord, res+1)
	chain[res] = c
	for i := res - 1; i >= 1; i-- {
		n := chain[i+1]
		chain[i] = cubeRoundFloat(float64(n.x)/2, float64(n.y)/2, float64(n.z)/2)
	}
	// chain[0] is the zero vector by definition.
	for i := 1; i <= res; i++ {
		diff := chain[i].sub(chain[i-1].double())
		digits[i-1] = digitFromDiff(diff)
	}
	return digits
}

// cubeFromDigits reconstructs the cube coordinate at resolution res from
// the hierarchical digit chain (inverse of digitsFromCube).
func cubeFromDigits(digits [MaxResolution]int, res int) cubeCoord {
	c := cubeCoord{}
	for i := 1; i <= res; i++ {
		c = c.double().add(digitDirection(digits[i-1]))
	}
	return c
}
