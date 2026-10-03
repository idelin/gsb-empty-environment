package hexgrid

import "math"

// LatLng is a geographic coordinate in degrees.
type LatLng struct {
	Lat float64 // latitude, clamped to [-90, 90]
	Lng float64 // longitude, wrapped to [-180, 180]
}

func llToVec3(ll LatLng) vec3 {
	lat := ll.Lat * math.Pi / 180
	lng := ll.Lng * math.Pi / 180
	return vec3{
		math.Cos(lat) * math.Cos(lng),
		math.Cos(lat) * math.Sin(lng),
		math.Sin(lat),
	}
}

func vec3ToLatLng(v vec3) LatLng {
	r := v3norm(v)
	return LatLng{
		Lat: math.Asin(v.z/r) * 180 / math.Pi,
		Lng: math.Atan2(v.y, v.x) * 180 / math.Pi,
	}
}

// cubeRoundFloat rounds a continuous lattice point to the nearest
// lattice point. Used only on the encoding path.
func cubeRoundFloat(x, y float64) vec2 {
	z := -x - y
	rx, ry, rz := math.Round(x), math.Round(y), math.Round(z)
	dx, dy, dz := math.Abs(rx-x), math.Abs(ry-y), math.Abs(rz-z)
	switch {
	case dx >= dy && dx >= dz:
		rx = -ry - rz
	case dy >= dz:
		ry = -rx - rz
	}
	return vec2{int64(rx), int64(ry)}
}

// FromLatLng encodes a geographic coordinate as the cell at the given
// resolution whose center is nearest to the gnomonic projection of the
// point onto its icosahedron face.
func FromLatLng(ll LatLng, res int) (Cell, error) {
	if res < 0 || res > MaxRes {
		return 0, ErrInvalidResolution
	}
	lat := math.Max(-90, math.Min(90, ll.Lat))
	lng := math.Mod(ll.Lng+540, 360) - 180
	v := llToVec3(LatLng{lat, lng})

	// Select the face whose cone contains the direction.
	face := 0
	best := -2.0
	for f := 0; f < 20; f++ {
		if d := v3dot(v, faceNormals[f]); d > best {
			best, face = d, f
		}
	}

	// Gnomonic projection onto the face plane.
	n := faceNormals[face]
	fv := icosaFaces[face]
	h := v3dot(icosaVertices[fv[0]], n)
	p := v3scale(v, h/v3dot(v, n))

	// Barycentric coordinates of the projected point.
	a := v3sub(icosaVertices[fv[1]], icosaVertices[fv[0]])
	b := v3sub(icosaVertices[fv[2]], icosaVertices[fv[0]])
	d := v3sub(p, icosaVertices[fv[0]])
	aa, ab, bb := v3dot(a, a), v3dot(a, b), v3dot(b, b)
	da, db := v3dot(d, a), v3dot(d, b)
	denom := aa*bb - ab*ab
	w1 := (da*bb - db*ab) / denom
	w2 := (db*aa - da*ab) / denom

	// Continuous lattice coords at frequency 7^res, then snap to the
	// nearest cell center of the resolution-res sublattice.
	fn := float64(freqTable[res])
	ci, cj := w1*fn, w2*fn
	for l := 0; l < res; l++ {
		ci, cj = (3*ci+cj)/7, (-ci+2*cj)/7
	}
	c := cubeRoundFloat(ci, cj)
	return cellFromCoord(face, c, res)
}

// Center returns the spherical center of the cell.
func (c Cell) Center() (LatLng, error) {
	face, w, err := cellToPoint(c)
	if err != nil {
		return LatLng{}, err
	}
	fn := float64(freqTable[c.Resolution()])
	fv := icosaFaces[face]
	p := v3add(
		v3add(
			v3scale(icosaVertices[fv[0]], float64(w[0])/fn),
			v3scale(icosaVertices[fv[1]], float64(w[1])/fn),
		),
		v3scale(icosaVertices[fv[2]], float64(w[2])/fn),
	)
	return vec3ToLatLng(p), nil
}
