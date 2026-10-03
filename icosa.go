package hexgrid

import "math"

// Icosahedron geometry: 12 vertices, 20 triangular faces.
// Each face carries a local gnomonic projection plane on which a
// 2D hexagonal lattice is laid out. A "base cell" is one icosahedron face.

const numFaces = 20

var icosaVertices [12]vec3

// icosaFaces lists vertex indices per face (outward orientation is not
// required; edge normals are oriented programmatically at init).
var icosaFaces = [numFaces][3]int{
	{0, 2, 8}, {0, 8, 4}, {0, 4, 6}, {0, 6, 9}, {0, 9, 2},
	{2, 5, 8}, {8, 5, 10}, {8, 10, 4}, {4, 10, 1}, {4, 1, 6},
	{6, 1, 11}, {6, 11, 9}, {9, 11, 7}, {9, 7, 2}, {2, 7, 5},
	{3, 10, 5}, {3, 1, 10}, {3, 11, 1}, {3, 7, 11}, {3, 5, 7},
}

var (
	faceCenters     [numFaces]vec3
	faceBasisU      [numFaces]vec3
	faceBasisV      [numFaces]vec3
	faceEdgeNormals [numFaces][3]vec3
)

func init() {
	phi := (1 + math.Sqrt(5)) / 2
	raw := [12]vec3{
		{0, 1, phi}, {0, 1, -phi}, {0, -1, phi}, {0, -1, -phi},
		{1, phi, 0}, {1, -phi, 0}, {-1, phi, 0}, {-1, -phi, 0},
		{phi, 0, 1}, {-phi, 0, 1}, {phi, 0, -1}, {-phi, 0, -1},
	}
	for i, v := range raw {
		icosaVertices[i] = v.normalized()
	}
	for f := 0; f < numFaces; f++ {
		a := icosaVertices[icosaFaces[f][0]]
		b := icosaVertices[icosaFaces[f][1]]
		c := icosaVertices[icosaFaces[f][2]]
		center := a.add(b).add(c).normalized()
		faceCenters[f] = center

		// Deterministic in-plane orthonormal basis (u, v) for the
		// tangent plane at the face center.
		ref := vec3{0, 0, 1}
		if math.Abs(center.dot(ref)) > 0.9 {
			ref = vec3{1, 0, 0}
		}
		u := center.cross(ref).normalized()
		faceBasisU[f] = u
		faceBasisV[f] = center.cross(u).normalized()

		// Edge normals of the planar triangle through the 3 vertices,
		// oriented so the face center is on the positive side.
		verts := [3]vec3{a, b, c}
		for e := 0; e < 3; e++ {
			v0 := verts[e]
			v1 := verts[(e+1)%3]
			n := v0.cross(v1).normalized()
			if n.dot(center) < 0 {
				n = n.scale(-1)
			}
			faceEdgeNormals[f][e] = n
		}
	}
}

// bestFace returns the index of the icosahedron face whose center is
// closest (max dot product) to the unit vector p.
func bestFace(p vec3) int {
	best, bestDot := 0, -2.0
	for f := 0; f < numFaces; f++ {
		if d := p.dot(faceCenters[f]); d > bestDot {
			best, bestDot = f, d
		}
	}
	return best
}

// pointInFace reports whether the gnomonic projection of p onto the face
// plane falls inside the face's planar triangle.
func pointInFace(face int, p vec3) bool {
	const eps = 1e-12
	for e := 0; e < 3; e++ {
		if p.dot(faceEdgeNormals[face][e]) < -eps {
			return false
		}
	}
	return true
}

// projectToFace performs a gnomonic projection of unit vector p onto the
// tangent plane of the given face, returning local 2D coordinates.
func projectToFace(face int, p vec3) (x, y float64) {
	c := faceCenters[face]
	t := p.scale(1 / p.dot(c))
	return t.dot(faceBasisU[face]), t.dot(faceBasisV[face])
}

// unprojectFromFace maps local 2D face-plane coordinates back to a unit
// vector on the sphere.
func unprojectFromFace(face int, x, y float64) vec3 {
	t := faceCenters[face].
		add(faceBasisU[face].scale(x)).
		add(faceBasisV[face].scale(y))
	return t.normalized()
}
