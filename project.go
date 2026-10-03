package hexgrid

import (
	"errors"
	"math"
)

// ErrInvalidLatLng is returned for NaN/Inf coordinates or |lat| > 90.
var ErrInvalidLatLng = errors.New("hexgrid: invalid latitude/longitude")

// LatLng is a geographic coordinate in degrees.
type LatLng struct {
	Lat, Lng float64
}

// levelBasis holds the 2D embedding of one hierarchy level: the hex
// lattice basis vectors b1, b2 at that level's scale and rotation.
// Going down one level scales by 1/sqrt(7) and rotates by -theta0,
// where theta0 = atan2(sin60, 2+cos60) ~ 19.1066 degrees; this is the
// geometric counterpart of the integer map M in ij.go.
type levelBasis struct {
	scale, rot         float64
	b1x, b1y, b2x, b2y float64
}

var levelGeom, theta0, scale0 = buildLevelGeom()

func buildLevelGeom() ([MaxRes + 1]levelBasis, float64, float64) {
	th := math.Atan2(math.Sin(math.Pi/3), 2+math.Cos(math.Pi/3))
	s0 := faceInradius * math.Sqrt(3)
	var g [MaxRes + 1]levelBasis
	for k := 0; k <= MaxRes; k++ {
		scale := s0 * math.Pow(7, -float64(k)/2)
		rot := -float64(k) * th
		g[k] = levelBasis{
			scale: scale,
			rot:   rot,
			b1x:   scale * math.Cos(rot),
			b1y:   scale * math.Sin(rot),
			b2x:   scale * math.Cos(rot+math.Pi/3),
			b2y:   scale * math.Sin(rot+math.Pi/3),
		}
	}
	return g, th, s0
}

// embed2D maps a level-k IJ offset to face-local 2D coordinates.
func embed2D(level int, off IJ) (x, y float64) {
	b := levelGeom[level]
	return float64(off.I)*b.b1x + float64(off.J)*b.b2x,
		float64(off.I)*b.b1y + float64(off.J)*b.b2y
}

func llToVec(latDeg, lngDeg float64) vec3 {
	lat := latDeg * math.Pi / 180
	lng := lngDeg * math.Pi / 180
	return vec3{math.Cos(lat) * math.Cos(lng), math.Cos(lat) * math.Sin(lng), math.Sin(lat)}
}

func normalizeLng(lng float64) float64 {
	lng = math.Mod(lng+180, 360)
	if lng < 0 {
		lng += 360
	}
	return lng - 180
}

// LatLngToCell encodes a geographic coordinate as a cell index at the
// given resolution. It selects the nearest icosahedron face, gnomonic
// projects onto its tangent plane, then quantizes the projected point
// onto the resolution-r hex lattice (exact cube rounding) and derives
// the digit path from the lattice coordinate.
//
// Points near icosahedron vertices fall outside every base cell's
// digit hierarchy at fine resolutions (this simplified system has no
// pentagons); for those a greedy nearest-child descent is used as a
// fallback so every coordinate on the globe still encodes.
func LatLngToCell(latDeg, lngDeg float64, res int) (Cell, error) {
	if res < 0 || res > MaxRes {
		return 0, ErrInvalidResolution
	}
	if math.IsNaN(latDeg) || math.IsNaN(lngDeg) ||
		math.IsInf(latDeg, 0) || math.IsInf(lngDeg, 0) ||
		latDeg < -90 || latDeg > 90 {
		return 0, ErrInvalidLatLng
	}
	p := llToVec(latDeg, normalizeLng(lngDeg))
	f := &faces[nearestFace(p)]
	x, y := gnomonicLocal(p, f)

	if digits, ok := ijToDigits(quantize2D(x, y, res), res); ok {
		return NewCell(f.ID, res, digits)
	}
	return greedyEncode(f.ID, x, y, res)
}

// quantize2D rounds a face-local 2D point to the nearest lattice point
// of the resolution-r hex grid using exact cube-coordinate rounding.
func quantize2D(x, y float64, res int) IJ {
	b := levelGeom[res]
	det := b.b1x*b.b2y - b.b1y*b.b2x
	fi := (x*b.b2y - y*b.b2x) / det
	fj := (b.b1x*y - b.b1y*x) / det
	// Cube coordinates: x=i, z=j, y=-i-j; round with largest-error fixup.
	fx, fz := fi, fj
	fy := -fx - fz
	rx, ry, rz := math.Round(fx), math.Round(fy), math.Round(fz)
	dx, dy, dz := math.Abs(rx-fx), math.Abs(ry-fy), math.Abs(rz-fz)
	switch {
	case dx >= dy && dx >= dz:
		rx = -ry - rz
	case dy >= dz:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return IJ{int(rx), int(rz)}
}

// greedyEncode descends the hierarchy picking the nearest child center
// at every level. Always succeeds; used when the exact lattice point is
// not representable inside the base cell's hierarchy.
func greedyEncode(baseCell int, x, y float64, res int) (Cell, error) {
	digits := make([]uint8, res)
	var cx, cy float64
	for k := 1; k <= res; k++ {
		best := 0
		bestDist := math.Inf(1)
		var bx, by float64
		for d := 0; d < 7; d++ {
			ox, oy := embed2D(k, digitOffsets[d])
			dx, dy := x-cx-ox, y-cy-oy
			if dist := dx*dx + dy*dy; dist < bestDist {
				best, bestDist, bx, by = d, dist, ox, oy
			}
		}
		cx += bx
		cy += by
		digits[k-1] = uint8(best)
	}
	return NewCell(baseCell, res, digits)
}

func nearestFace(p vec3) int {
	best := 0
	bestDot := math.Inf(-1)
	for i := range faces {
		if d := p.dot(faces[i].Center); d > bestDot {
			best, bestDot = i, d
		}
	}
	return best
}

// gnomonicLocal projects a unit vector onto the face tangent plane and
// returns face-local 2D coordinates.
func gnomonicLocal(p vec3, f *face) (x, y float64) {
	t := p.scale(1 / p.dot(f.Center)).sub(f.Center)
	return t.dot(f.U), t.dot(f.V)
}

// cellCenter2D returns the face-local 2D center of a cell by summing
// its digit embeddings (identical to embedding the folded IJ).
func cellCenter2D(c Cell) (x, y float64) {
	res := c.Res()
	for k := 1; k <= res; k++ {
		ox, oy := embed2D(k, digitOffsets[c.Digit(k)])
		x += ox
		y += oy
	}
	return x, y
}

func unproject(f *face, x, y float64) LatLng {
	v := f.Center.add(f.U.scale(x)).add(f.V.scale(y)).norm()
	lat, lng := vecToLatLngRad(v)
	return LatLng{Lat: lat * 180 / math.Pi, Lng: lng * 180 / math.Pi}
}

// CellToLatLng returns the geographic center of a cell.
func CellToLatLng(c Cell) LatLng {
	f := &faces[c.BaseCell()]
	x, y := cellCenter2D(c)
	return unproject(f, x, y)
}

// cellBoundary2D returns the 6 hexagon vertices of a cell in face-local
// 2D coordinates (counter-clockwise).
func cellBoundary2D(c Cell) [6][2]float64 {
	res := c.Res()
	cx, cy := cellCenter2D(c)
	r := levelGeom[res].scale / math.Sqrt(3)
	rot := levelGeom[res].rot
	var out [6][2]float64
	for m := 0; m < 6; m++ {
		ang := rot + math.Pi/6 + float64(m)*math.Pi/3
		out[m] = [2]float64{cx + r*math.Cos(ang), cy + r*math.Sin(ang)}
	}
	return out
}

// CellToBoundary returns the 6 geographic vertices of a cell.
func CellToBoundary(c Cell) []LatLng {
	f := &faces[c.BaseCell()]
	b := cellBoundary2D(c)
	out := make([]LatLng, 6)
	for i, p := range b {
		out[i] = unproject(f, p[0], p[1])
	}
	return out
}
