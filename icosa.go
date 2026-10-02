package hexgrid

import "math"

// Icosahedron model: 12 vertices (2 poles + 2 rings of 5) and 20 triangular
// faces. Points are assigned to faces by gnomonic projection, which for a
// regular icosahedron is equivalent to picking the face whose center normal
// has the maximum dot product with the point.

const (
	numFaces         = 20
	rootHexesPerFace = 7
	numBaseCells     = numFaces * rootHexesPerFace // 140
)

type faceGeom struct {
	n  vec3 // outward unit normal through the face center (also the plane foot)
	e1 vec3 // in-plane orthonormal basis
	e2 vec3
}

var faces [numFaces]faceGeom

// faceVertexDist is the in-plane distance from a face center to a vertex
// (identical for all 20 faces of a regular icosahedron).
var faceVertexDist float64

func initFaces() {
	// Vertices of a regular icosahedron inscribed in the unit sphere.
	// Ring latitude: atan(1/2) => sin = 1/sqrt(5), cos = 2/sqrt(5).
	sinT := 1 / math.Sqrt(5)
	cosT := 2 / math.Sqrt(5)
	north := vec3{0, 0, 1}
	south := vec3{0, 0, -1}
	var top, bot [5]vec3
	for i := 0; i < 5; i++ {
		lt := float64(i) * 72 * math.Pi / 180
		top[i] = vec3{cosT * math.Cos(lt), cosT * math.Sin(lt), sinT}
		lb := (float64(i)*72 + 36) * math.Pi / 180
		bot[i] = vec3{cosT * math.Cos(lb), cosT * math.Sin(lb), -sinT}
	}
	var tris [numFaces][3]vec3
	n := 0
	for i := 0; i < 5; i++ {
		j := (i + 1) % 5
		tris[n] = [3]vec3{north, top[i], top[j]}
		n++
		tris[n] = [3]vec3{south, bot[j], bot[i]}
		n++
		tris[n] = [3]vec3{top[i], bot[i], top[j]}
		n++
		tris[n] = [3]vec3{bot[i], top[j], bot[j]}
		n++
	}
	for f := 0; f < numFaces; f++ {
		a, b, c := tris[f][0], tris[f][1], tris[f][2]
		fn := a.add(b).add(c).norm()
		e1 := a.sub(fn.scale(a.dot(fn))).norm()
		e2 := fn.cross(e1)
		faces[f] = faceGeom{n: fn, e1: e1, e2: e2}
		if f == 0 {
			// Gnomonic projection of vertex a onto the plane {v: v.n = 1}.
			faceVertexDist = a.scale(1 / a.dot(fn)).sub(fn).length()
		}
	}
}

// bestFace returns the index of the face whose gnomonic projection contains p.
func bestFace(p vec3) int {
	best := 0
	bestDot := -2.0
	for i := 0; i < numFaces; i++ {
		if d := p.dot(faces[i].n); d > bestDot {
			best, bestDot = i, d
		}
	}
	return best
}

// projectFace maps a unit vector to 2D gnomonic coordinates on face f.
func projectFace(f int, p vec3) (x, y float64) {
	g := &faces[f]
	t := 1 / p.dot(g.n)
	l := p.scale(t).sub(g.n)
	return l.dot(g.e1), l.dot(g.e2)
}

// unprojectFace maps 2D face coordinates back to a unit vector.
func unprojectFace(f int, x, y float64) vec3 {
	g := &faces[f]
	return g.n.add(g.e1.scale(x)).add(g.e2.scale(y)).norm()
}
