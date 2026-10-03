package hexgrid

import "math"

// Icosahedron geometry and the exact integer edge-crossing tables that
// glue the 20 face-local triangular lattices into one closed manifold
// grid. All runtime topology (K-ring, hierarchy) uses only these
// integer tables; the floating point geometry below runs once at init
// and inside the geographic projection.

type vec3 struct {
	x, y, z float64
}

func v3add(a, b vec3) vec3           { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func v3sub(a, b vec3) vec3           { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func v3scale(a vec3, s float64) vec3 { return vec3{a.x * s, a.y * s, a.z * s} }
func v3dot(a, b vec3) float64        { return a.x*b.x + a.y*b.y + a.z*b.z }
func v3cross(a, b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}
func v3norm(a vec3) float64 { return math.Sqrt(v3dot(a, a)) }
func v3unit(a vec3) vec3    { return v3scale(a, 1/v3norm(a)) }

var icosaVertices [12]vec3
var icosaFaces [20][3]int

// faceNormals[f] is the outward unit normal of face f.
var faceNormals [20]vec3

// crossRef identifies the neighbor face across one edge and the slot of
// the opposite vertex of that edge in the neighbor's vertex triple.
type crossRef struct {
	face int
	opp  int
}

// faceCross[f][m] describes the edge of face f opposite its vertex slot m.
var faceCross [20][3]crossRef

func init() {
	phi := (1 + math.Sqrt(5)) / 2
	icosaVertices = [12]vec3{
		{-1, phi, 0}, {1, phi, 0}, {-1, -phi, 0}, {1, -phi, 0},
		{0, -1, phi}, {0, 1, phi}, {0, -1, -phi}, {0, 1, -phi},
		{phi, 0, -1}, {phi, 0, 1}, {-phi, 0, -1}, {-phi, 0, 1},
	}
	icosaFaces = [20][3]int{
		{0, 11, 5}, {0, 5, 1}, {0, 1, 7}, {0, 7, 10}, {0, 10, 11},
		{1, 5, 9}, {5, 11, 4}, {11, 10, 2}, {10, 7, 6}, {7, 1, 8},
		{3, 9, 4}, {3, 4, 2}, {3, 2, 6}, {3, 6, 8}, {3, 8, 9},
		{4, 9, 5}, {2, 4, 11}, {6, 2, 10}, {8, 6, 7}, {9, 8, 1},
	}
	// Force counter-clockwise vertex order seen from outside.
	for f, face := range icosaFaces {
		a := icosaVertices[face[0]]
		b := icosaVertices[face[1]]
		c := icosaVertices[face[2]]
		n := v3cross(v3sub(b, a), v3sub(c, a))
		if v3dot(n, v3add(v3add(a, b), c)) < 0 {
			icosaFaces[f][1], icosaFaces[f][2] = icosaFaces[f][2], icosaFaces[f][1]
		}
	}
	for f, face := range icosaFaces {
		a := icosaVertices[face[0]]
		b := icosaVertices[face[1]]
		c := icosaVertices[face[2]]
		faceNormals[f] = v3unit(v3cross(v3sub(b, a), v3sub(c, a)))
	}
	// Build adjacency: every edge is shared by exactly two faces.
	type edgeKey struct{ a, b int }
	type edgeRef struct{ face, opp int }
	edges := make(map[edgeKey][]edgeRef)
	for f, face := range icosaFaces {
		for opp := 0; opp < 3; opp++ {
			a, b := face[(opp+1)%3], face[(opp+2)%3]
			if a > b {
				a, b = b, a
			}
			k := edgeKey{a, b}
			edges[k] = append(edges[k], edgeRef{f, opp})
		}
	}
	if len(edges) != 30 {
		panic("hexgrid: icosahedron must have 30 edges")
	}
	for _, refs := range edges {
		if len(refs) != 2 {
			panic("hexgrid: edge must be shared by exactly 2 faces")
		}
		a, b := refs[0], refs[1]
		faceCross[a.face][a.opp] = crossRef{b.face, b.opp}
		faceCross[b.face][b.opp] = crossRef{a.face, a.opp}
	}
}

// baryFromIJ converts lattice coords (i, j) at frequency n to integer
// barycentric coords (w0, w1, w2) with w0+w1+w2 = n.
func baryFromIJ(p vec2, n int64) [3]int64 {
	return [3]int64{n - p.i - p.j, p.i, p.j}
}

// ijFromBary is the inverse of baryFromIJ.
func ijFromBary(w [3]int64) vec2 {
	return vec2{w[1], w[2]}
}

// crossEdge maps a barycentric point on face f across the edge opposite
// vertex slot m into the neighbor face. Exact integer transform derived
// from unfolding the two faces into one plane.
func crossEdge(f, m int, w [3]int64) (int, [3]int64) {
	ref := faceCross[f][m]
	var nw [3]int64
	nw[ref.opp] = -w[m]
	nw[(ref.opp+1)%3] = w[(m+2)%3] + w[m]
	nw[(ref.opp+2)%3] = w[(m+1)%3] + w[m]
	return ref.face, nw
}

const maxCrossings = 64

// normalize walks a barycentric point back inside its face triangle by
// crossing edges until all barycentric coords are non-negative.
func normalize(f int, w [3]int64) (int, [3]int64, error) {
	for n := 0; ; n++ {
		m := -1
		var minVal int64
		for c := 0; c < 3; c++ {
			if w[c] < 0 && (m < 0 || w[c] < minVal) {
				m, minVal = c, w[c]
			}
		}
		if m < 0 {
			return f, w, nil
		}
		if n >= maxCrossings {
			return 0, w, errNormalization
		}
		f, w = crossEdge(f, m, w)
	}
}

// canonicalize re-expresses a point that lies exactly on an edge or
// vertex on the lowest-numbered face that contains it, so every
// physical cell has exactly one canonical coordinate representation.
// All faces sharing the point are enumerated by walking across the
// zero-coordinate edges (up to 5 faces meet at an icosahedron vertex).
func canonicalize(f int, w [3]int64) (int, [3]int64) {
	type rep struct {
		f int
		w [3]int64
	}
	bestF, bestW := f, w
	visited := [20]bool{}
	visited[f] = true
	queue := []rep{{f, w}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.f < bestF {
			bestF, bestW = cur.f, cur.w
		}
		for c := 0; c < 3; c++ {
			if cur.w[c] != 0 {
				continue
			}
			nf, nw := crossEdge(cur.f, c, cur.w)
			if !visited[nf] {
				visited[nf] = true
				queue = append(queue, rep{nf, nw})
			}
		}
	}
	return bestF, bestW
}
