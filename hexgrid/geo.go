package hexgrid

import "math"

// latLonToVec converts geographic coordinates (degrees) to a unit vector.
func latLonToVec(lat, lon float64) vec3 {
	la := lat * math.Pi / 180
	lo := lon * math.Pi / 180
	return vec3{
		math.Cos(la) * math.Cos(lo),
		math.Cos(la) * math.Sin(lo),
		math.Sin(la),
	}
}

func vecToLatLon(v vec3) (lat, lon float64) {
	v = v.norm()
	lat = math.Asin(v.z) * 180 / math.Pi
	lon = math.Atan2(v.y, v.x) * 180 / math.Pi
	return lat, lon
}

// findFace returns the icosahedron face containing the direction p.
func (g *Grid) findFace(p vec3) int {
	best, bestDot := 0, -2.0
	for f := 0; f < NumFaces; f++ {
		if d := p.dot(g.normals[f]); d > bestDot {
			best, bestDot = f, d
		}
	}
	return best
}

// barycentric computes the barycentric coordinates of p with respect to the
// 3D triangle (a, b, c). p is assumed to lie in (or near) the triangle plane.
func barycentric(p, a, b, c vec3) (u, v, w float64) {
	v0 := c.sub(a)
	v1 := b.sub(a)
	v2 := p.sub(a)
	d00 := v0.dot(v0)
	d01 := v0.dot(v1)
	d11 := v1.dot(v1)
	d20 := v2.dot(v0)
	d21 := v2.dot(v1)
	denom := d00*d11 - d01*d01
	u = (d11*d20 - d01*d21) / denom // weight of c
	v = (d00*d21 - d01*d20) / denom // weight of b
	w = 1 - u - v                   // weight of a
	return u, v, w
}

// faceToRhombusCoords maps a direction p on face f to the local (a, b)
// coordinates of the rhombus containing f, with a, b in [0, 1].
//
// The rhombus frame is p = V0 + a*(V1-V0) + b*(V3-V0) in the unfolded
// parallelogram. On face F0 = (V0,V1,V2) with barycentric (w,v,u) the
// coords are a = v+u, b = u; on face F1 = (V0,V2,V3) they are a = v, b = v+u.
func (g *Grid) faceToRhombusCoords(f int, p vec3) (rh int, a, b float64) {
	rh = g.faceRhombus[f]
	rv := g.rhombusVerts[rh]
	V0 := g.verts[rv[0]]
	// Project p radially onto the face plane.
	n := g.normals[f]
	t := n.dot(V0) / n.dot(p)
	pp := p.scale(t)
	if g.rhombusFaces[rh][0] == f {
		// F0 = (V0, V1, V2): a = w1 + w2, b = w2.
		w2, w1, _ := barycentric(pp, V0, g.verts[rv[1]], g.verts[rv[2]])
		a = w1 + w2
		b = w2
	} else {
		// F1 = (V0, V2, V3): a = w2, b = w2 + w3.
		w3, w2, _ := barycentric(pp, V0, g.verts[rv[2]], g.verts[rv[3]])
		a = w2
		b = w2 + w3
	}
	return rh, a, b
}

// FromLatLon encodes a geographic coordinate (degrees) as the hexagon cell
// at the given resolution containing it.
func (g *Grid) FromLatLon(lat, lon float64, res int) CellID {
	if res < 0 || res > MaxResolution {
		panic("hexgrid: resolution out of range")
	}
	p := latLonToVec(lat, lon)
	f := g.findFace(p)
	rh, a, b := g.faceToRhombusCoords(f, p)
	n := float64(int(1) << uint(res))
	q := int(a * n)
	r := int(b * n)
	max := int(1)<<uint(res) - 1
	if q < 0 {
		q = 0
	} else if q > max {
		q = max
	}
	if r < 0 {
		r = 0
	} else if r > max {
		r = max
	}
	return MakeCell(rh, res, q, r)
}

// rhombusCoordsToLatLon maps rhombus-local (a, b) coordinates back to a
// geographic position by inverting the affine frame on the owning face.
func (g *Grid) rhombusCoordsToLatLon(rh int, a, b float64) (lat, lon float64) {
	rv := g.rhombusVerts[rh]
	V0 := g.verts[rv[0]]
	V1 := g.verts[rv[1]]
	V2 := g.verts[rv[2]]
	V3 := g.verts[rv[3]]
	var p vec3
	if a >= b {
		// F0 = (V0, V1, V2): barycentric (1-a, a-b, b).
		p = V0.scale(1 - a).add(V1.scale(a - b)).add(V2.scale(b))
	} else {
		// F1 = (V0, V2, V3): barycentric (1-b, a, b-a).
		p = V0.scale(1 - b).add(V2.scale(a)).add(V3.scale(b - a))
	}
	return vecToLatLon(p)
}

// Center returns the geographic center (degrees) of the cell.
func (g *Grid) Center(c CellID) (lat, lon float64) {
	n := float64(int(1) << uint(c.Resolution()))
	a := (float64(c.Q()) + 0.5) / n
	b := (float64(c.R()) + 0.5) / n
	return g.rhombusCoordsToLatLon(c.Rhombus(), a, b)
}
