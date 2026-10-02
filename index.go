package hexgrid

import "math"

const (
	degToRad = math.Pi / 180
	radToDeg = 180 / math.Pi
)

// LatLngToCell encodes a WGS84 coordinate (degrees) into the cell at the
// given resolution (clamped to 0..15). Allocates nothing.
func LatLngToCell(lat, lng float64, res int) Cell {
	if res < 0 {
		res = 0
	}
	if res > maxRes {
		res = maxRes
	}
	phi := lat * degToRad
	lambda := lng * degToRad
	cosPhi := math.Cos(phi)
	p := vec3{cosPhi * math.Cos(lambda), cosPhi * math.Sin(lambda), math.Sin(phi)}
	return cellFromVec(p, res)
}

// cellFromVec encodes a unit vector into a cell. Trig-free: only dot
// products and one sqrt (vector normalisation is not even needed here
// because gnomonic projection is ratio-based).
func cellFromVec(p vec3, res int) Cell {
	f := bestFace(p)
	x, y := projectFace(f, p)
	q, r := cartToAxial(x, y, res)
	root, q0, r0, digits := reduceAxial(q, r, res)
	if root < 0 {
		root = nearestRoot(q0, r0)
	}
	return NewCell(f, root, res, digits)
}

// CellToLatLng returns the latitude/longitude (degrees) of the cell center.
func CellToLatLng(c Cell) (lat, lng float64) {
	face, q, r, res := axialOf(c)
	x, y := axialToCartBase(q, r, res)
	p := unprojectFace(face, x, y)
	return vecToLatLng(p)
}

// vecToLatLng converts a unit vector to latitude/longitude in degrees.
func vecToLatLng(v vec3) (lat, lng float64) {
	lat = math.Atan2(v.z, math.Sqrt(v.x*v.x+v.y*v.y)) * radToDeg
	lng = math.Atan2(v.y, v.x) * radToDeg
	return lat, lng
}

// axialOf decodes a cell into its face and absolute axial coordinates at
// the cell's own resolution. Pure integer arithmetic.
func axialOf(c Cell) (face int, q, r int64, res int) {
	face = c.Face()
	root := c.BaseCell() % rootHexesPerFace
	res = c.Resolution()
	q, r = buildAxial(root, c.digits(), res)
	return face, q, r, res
}
