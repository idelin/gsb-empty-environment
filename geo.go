package hexgrid

// GeoToCell encodes a geographic coordinate (degrees) as a hierarchical
// hexagonal grid cell at the given resolution (0..MaxResolution).
//
// Pipeline: lat/lng -> unit sphere -> nearest icosahedron face ->
// gnomonic projection onto the face plane -> hexagonal lattice
// quantization -> hierarchical 7-ary digit chain.
func GeoToCell(latDeg, lngDeg float64, res int) Cell {
	if res < 0 {
		res = 0
	}
	if res > MaxResolution {
		res = MaxResolution
	}
	p := latLngToVec(latDeg, lngDeg)
	face := bestFace(p)
	px, py := projectToFace(face, p)
	c := planeToCube(px, py, res)
	return makeCell(face, res, digitsFromCube(c, res))
}

// cellToCube decodes a cell into its face, resolution and cube coordinate.
func cellToCube(c Cell) (face, res int, cc cubeCoord) {
	res = c.Resolution()
	return c.BaseCell(), res, cubeFromDigits(c.digits(), res)
}

// cubeToCell encodes a cube coordinate on a given face back into a Cell.
// If the coordinate's center falls outside the face triangle (possible
// when traversing across icosahedron edges), it is re-encoded through
// the global spherical path so the result always lands on the correct
// face.
func cubeToCell(face, res int, c cubeCoord) Cell {
	px, py := cubeToPlane(c, res)
	p := unprojectFromFace(face, px, py)
	if !pointInFace(face, p) {
		lat, lng := vecToLatLng(p)
		return GeoToCell(lat, lng, res)
	}
	return makeCell(face, res, digitsFromCube(c, res))
}

// Center returns the latitude/longitude (degrees) of the cell's center.
func (c Cell) Center() (latDeg, lngDeg float64) {
	face, res, cc := cellToCube(c)
	px, py := cubeToPlane(cc, res)
	return vecToLatLng(unprojectFromFace(face, px, py))
}
