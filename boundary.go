package hexgrid

import "math"

// LatLng is a geographic coordinate in degrees.
type LatLng struct {
	Lat, Lng float64
}

// hexVertexOffsets are the 6 vertex offsets of a unit hexagon in the 2D
// hex-grid plane, at angles 30, 90, ..., 330 degrees with radius 1/sqrt(3).
var hexVertexOffsets [6][2]float64

func init() {
	r := 1 / math.Sqrt(3)
	for m := 0; m < 6; m++ {
		a := (30 + 60*float64(m)) * math.Pi / 180
		hexVertexOffsets[m][0] = r * math.Cos(a)
		hexVertexOffsets[m][1] = r * math.Sin(a)
	}
}

// Boundary returns the six geographic vertices of the cell, in order.
// Shared edges between adjacent cells produce identical vertices (up to
// floating-point rounding), which tests use to verify continuous coverage.
func (c Cell) Boundary() [6]LatLng {
	face, res, coord := decodeCell(c)
	cx, cy := ijkToHex2d(coord)
	g := faces[face]
	var out [6]LatLng
	for m := 0; m < 6; m++ {
		x := (cx + hexVertexOffsets[m][0]) * scaleByRes[res]
		y := (cy + hexVertexOffsets[m][1]) * scaleByRes[res]
		dir := g.n.add(g.u.scale(x)).add(g.v.scale(y)).unit()
		out[m] = LatLng{
			Lat: math.Atan2(dir.z, math.Hypot(dir.x, dir.y)) * 180 / math.Pi,
			Lng: math.Atan2(dir.y, dir.x) * 180 / math.Pi,
		}
	}
	return out
}
