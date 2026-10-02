// Package grid implements a discrete hexagonal hierarchical spatial index
// built on an icosahedron unfolding of the sphere, together with pure
// integer (no floating point) topological K-ring neighborhood traversal.
package grid

import "math"

// LatLng is a geographic coordinate in degrees.
type LatLng struct {
	Lat float64
	Lng float64
}

type vec3 struct {
	X, Y, Z float64
}

func (v vec3) dot(o vec3) float64   { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }
func (v vec3) scale(s float64) vec3 { return vec3{v.X * s, v.Y * s, v.Z * s} }
func (v vec3) add(o vec3) vec3      { return vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v vec3) sub(o vec3) vec3      { return vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v vec3) cross(o vec3) vec3 {
	return vec3{
		v.Y*o.Z - v.Z*o.Y,
		v.Z*o.X - v.X*o.Z,
		v.X*o.Y - v.Y*o.X,
	}
}
func (v vec3) norm() vec3 {
	l := math.Sqrt(v.dot(v))
	return v.scale(1 / l)
}

// face is one icosahedron face tangent-plane frame.
type face struct {
	normal vec3 // outward unit normal through the face centroid
	e1, e2 vec3 // orthonormal in-plane basis
}

var icoFaces [numFaces]face

// buildIcosahedron derives the 20 faces from the 12 icosahedron vertices by
// enumerating mutually adjacent vertex triples, so no face table is
// transcribed by hand.
func buildIcosahedron() {
	phi := (1 + math.Sqrt(5)) / 2
	verts := []vec3{
		{0, 1, phi}, {0, 1, -phi}, {0, -1, phi}, {0, -1, -phi},
		{1, phi, 0}, {1, -phi, 0}, {-1, phi, 0}, {-1, -phi, 0},
		{phi, 0, 1}, {phi, 0, -1}, {-phi, 0, 1}, {-phi, 0, -1},
	}
	isEdge := func(a, b vec3) bool {
		d := a.sub(b)
		d2 := d.dot(d)
		// edge length of this icosahedron is exactly 2
		return math.Abs(d2-4) < 1e-9
	}
	idx := 0
	for i := 0; i < len(verts) && idx < numFaces; i++ {
		for j := i + 1; j < len(verts) && idx < numFaces; j++ {
			for k := j + 1; k < len(verts) && idx < numFaces; k++ {
				if !isEdge(verts[i], verts[j]) || !isEdge(verts[j], verts[k]) || !isEdge(verts[i], verts[k]) {
					continue
				}
				n := verts[i].add(verts[j]).add(verts[k]).norm()
				e1 := verts[i].sub(n.scale(verts[i].dot(n))).norm()
				e2 := n.cross(e1)
				icoFaces[idx] = face{normal: n, e1: e1, e2: e2}
				idx++
			}
		}
	}
	if idx != numFaces {
		panic("grid: icosahedron face enumeration failed")
	}
}

func latLngToVec3(ll LatLng) vec3 {
	lat := ll.Lat * math.Pi / 180
	lng := ll.Lng * math.Pi / 180
	return vec3{
		X: math.Cos(lat) * math.Cos(lng),
		Y: math.Cos(lat) * math.Sin(lng),
		Z: math.Sin(lat),
	}
}

func vec3ToLatLng(v vec3) LatLng {
	return LatLng{
		Lat: math.Atan2(v.Z, math.Hypot(v.X, v.Y)) * 180 / math.Pi,
		Lng: math.Atan2(v.Y, v.X) * 180 / math.Pi,
	}
}

// faceOf returns the face whose normal has the maximum dot product with p.
// Ties (points exactly on a face edge) resolve to the lowest face index,
// which keeps the assignment deterministic.
func faceOf(p vec3) int {
	best, bestDot := 0, -2.0
	for f := 0; f < numFaces; f++ {
		if d := p.dot(icoFaces[f].normal); d > bestDot {
			best, bestDot = f, d
		}
	}
	return best
}

// project applies the gnomonic projection of unit vector p onto the tangent
// plane of face f, returning 2D face-plane coordinates.
func project(f int, p vec3) (x, y float64) {
	fc := icoFaces[f]
	dn := p.dot(fc.normal)
	return p.dot(fc.e1) / dn, p.dot(fc.e2) / dn
}

// unproject inverts project: face-plane (x, y) back to a unit vector.
func unproject(f int, x, y float64) vec3 {
	fc := icoFaces[f]
	return fc.normal.add(fc.e1.scale(x)).add(fc.e2.scale(y)).norm()
}

const (
	sqrt3      = 1.7320508075688772935
	sqrt3over2 = 0.8660254037844386468
	sqrt7      = 2.6457513105737893505
)

// scale0 is the res-0 grid scale: hex center-to-center distance on the face
// tangent plane is 1/scale0. It is chosen so that every point of every face
// triangle falls within hex distance 1 of the face origin, i.e. the res-0
// leading digit is always in [0,6].
const scale0 = 1.2

// scales[r] = scale0 * 7^(r/2): the aperture-7 hierarchy zoom factor.
var scales [MaxRes + 1]float64

// cosPhi[r], sinPhi[r] encode the cumulative grid rotation Phi(r) of the
// resolution-r lattice relative to the face frame. Odd levels apply the
// aperture-7 "r" transform (rotation atan2(sqrt3, 2)), even levels the plain
// transform (rotation atan2(sqrt3/2, 5/2)); the two sum to 60 degrees so the
// lattice realigns every two resolutions.
var cosPhi [MaxRes + 1]float64
var sinPhi [MaxRes + 1]float64

func init() {
	buildIcosahedron()
	alphaOdd := math.Atan2(sqrt3, 2)
	alphaEven := math.Atan2(sqrt3/2, 2.5)
	phi := 0.0
	s := scale0
	for r := 0; r <= MaxRes; r++ {
		if r > 0 {
			if r%2 == 1 {
				phi -= alphaOdd
			} else {
				phi -= alphaEven
			}
			s *= sqrt7
		}
		scales[r] = s
		cosPhi[r] = math.Cos(phi)
		sinPhi[r] = math.Sin(phi)
	}
}

// quantize maps face-plane coordinates to the nearest res-r lattice point.
func quantize(x, y float64, res int) IJ {
	// rotate by -Phi(res), then scale
	rx := cosPhi[res]*x + sinPhi[res]*y
	ry := -sinPhi[res]*x + cosPhi[res]*y
	return hexRound(scales[res]*rx, scales[res]*ry)
}

// ijToFaceXY maps a res-r lattice point back to face-plane coordinates.
func ijToFaceXY(ij IJ, res int) (x, y float64) {
	s := scales[res]
	mx := (float64(ij.I) + 0.5*float64(ij.J)) / s
	my := (sqrt3over2 * float64(ij.J)) / s
	// rotate by +Phi(res)
	x = cosPhi[res]*mx - sinPhi[res]*my
	y = sinPhi[res]*mx + cosPhi[res]*my
	return x, y
}

// hexRound rounds scaled plane coordinates to the nearest hex lattice point
// using cube-coordinate rounding.
func hexRound(x, y float64) IJ {
	fj := 2 * y / sqrt3
	fi := x - y/sqrt3
	fk := -fi - fj
	ri := math.Round(fi)
	rj := math.Round(fj)
	rk := math.Round(fk)
	di := math.Abs(ri - fi)
	dj := math.Abs(rj - fj)
	dk := math.Abs(rk - fk)
	switch {
	case di >= dj && di >= dk:
		ri = -rj - rk
	case dj >= dk:
		rj = -ri - rk
	}
	return IJ{I: int(ri), J: int(rj)}
}

// CellToLatLng returns the geographic center of a cell.
func CellToLatLng(c Cell) LatLng {
	x, y := ijToFaceXY(c.IJ(), c.Res())
	return vec3ToLatLng(unproject(c.Face(), x, y))
}

// CellVertexRadius returns an upper bound, in radians, on the angular
// distance from a cell center to any point of the cell at the given
// resolution (including gnomonic distortion margin).
func CellVertexRadius(res int) float64 {
	return 2.0 * (1 / sqrt3) / scales[res]
}
