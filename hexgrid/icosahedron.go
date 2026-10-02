// Package hexgrid implements a discrete hexagon hierarchical spatial index
// built on an unfolded icosahedron. The icosahedron's 20 triangular faces
// are paired into 10 rhombi; each rhombus carries an n x n (n = 2^res)
// hexagonal cell lattice addressed by axial coordinates (q, r).
//
// All neighborhood traversal (K-Ring) is performed with pure integer
// arithmetic on precomputed edge-transition tables: no floating point and
// no trigonometry is used after a cell has been encoded.
package hexgrid

import "math"

const (
	// NumRhombi is the number of rhombus regions covering the icosahedron.
	NumRhombi = 10
	// NumFaces is the number of triangular icosahedron faces.
	NumFaces = 20
	// MaxResolution is the finest supported resolution (n = 2^res cells
	// per rhombus edge).
	MaxResolution = 15
)

type vec3 struct{ x, y, z float64 }

func (v vec3) add(o vec3) vec3      { return vec3{v.x + o.x, v.y + o.y, v.z + o.z} }
func (v vec3) sub(o vec3) vec3      { return vec3{v.x - o.x, v.y - o.y, v.z - o.z} }
func (v vec3) scale(s float64) vec3 { return vec3{v.x * s, v.y * s, v.z * s} }
func (v vec3) dot(o vec3) float64   { return v.x*o.x + v.y*o.y + v.z*o.z }
func (v vec3) cross(o vec3) vec3 {
	return vec3{v.y*o.z - v.z*o.y, v.z*o.x - v.x*o.z, v.x*o.y - v.y*o.x}
}
func (v vec3) norm() vec3 {
	l := math.Sqrt(v.dot(v))
	return v.scale(1 / l)
}

// edgeTransition describes how the hex lattice continues when crossing one
// of a rhombus's four boundary edges into the adjacent rhombus.
type edgeTransition struct {
	rhombus int  // adjacent rhombus across the edge
	edge    int  // which edge of the adjacent rhombus we arrive on
	flip    bool // whether the along-edge parameter is reversed
}

// Grid holds the precomputed, immutable topology tables of the icosahedral
// hexagon grid. A Grid is safe for concurrent use after construction.
type Grid struct {
	verts   [12]vec3
	faces   [NumFaces][3]int // vertex indices, counter-clockwise seen from outside
	normals [NumFaces]vec3

	faceRhombus  [NumFaces]int
	rhombusFaces [NumRhombi][2]int
	rhombusVerts [NumRhombi][4]int // V0,V1,V2,V3; local coords p = V0 + a*(V1-V0) + b*(V3-V0)

	transitions [NumRhombi][4]edgeTransition
}

// rhombus edge parameterization (P -> Q is the direction of parameter t):
//   edge 0: b = 0, P = V0, Q = V1, t = a
//   edge 1: a = 1, P = V1, Q = V2, t = b
//   edge 2: b = 1, P = V3, Q = V2, t = a
//   edge 3: a = 0, P = V0, Q = V3, t = b
var rhombusEdgeVerts = [4][2]int{{0, 1}, {1, 2}, {3, 2}, {0, 3}}

// NewGrid builds the icosahedron topology and all transition tables.
func NewGrid() *Grid {
	g := &Grid{}
	phi := (1 + math.Sqrt(5)) / 2
	raw := [12]vec3{
		{0, 1, phi}, {0, 1, -phi}, {0, -1, phi}, {0, -1, -phi},
		{1, phi, 0}, {1, -phi, 0}, {-1, phi, 0}, {-1, -phi, 0},
		{phi, 0, 1}, {-phi, 0, 1}, {phi, 0, -1}, {-phi, 0, -1},
	}
	for i, v := range raw {
		g.verts[i] = v.norm()
	}
	g.buildFaces()
	g.buildRhombi()
	g.buildTransitions()
	return g
}

// buildFaces finds the 20 triangular faces as triples of mutually adjacent
// vertices and orients each face outward.
func (g *Grid) buildFaces() {
	var adjacent [12][12]bool
	for i := 0; i < 12; i++ {
		for j := i + 1; j < 12; j++ {
			d := g.verts[i].sub(g.verts[j]).dot(g.verts[i].sub(g.verts[j]))
			if d < 2 { // normalized edge length squared ~1.105; next nearest ~2.894
				adjacent[i][j] = true
				adjacent[j][i] = true
			}
		}
	}
	n := 0
	for i := 0; i < 12; i++ {
		for j := i + 1; j < 12; j++ {
			if !adjacent[i][j] {
				continue
			}
			for k := j + 1; k < 12; k++ {
				if !adjacent[i][k] || !adjacent[j][k] {
					continue
				}
				f := [3]int{i, j, k}
				centroid := g.verts[i].add(g.verts[j]).add(g.verts[k])
				normal := g.verts[j].sub(g.verts[i]).cross(g.verts[k].sub(g.verts[i]))
				if normal.dot(centroid) < 0 {
					f[1], f[2] = f[2], f[1]
					normal = g.verts[f[1]].sub(g.verts[f[0]]).cross(g.verts[f[2]].sub(g.verts[f[0]]))
				}
				g.faces[n] = f
				g.normals[n] = normal.norm()
				n++
			}
		}
	}
	if n != NumFaces {
		panic("icosahedron: expected 20 faces")
	}
}

// buildRhombi pairs the 20 faces into 10 rhombi via a perfect matching on
// the face-adjacency graph, then derives each rhombus's vertex frame.
func (g *Grid) buildRhombi() {
	// Face adjacency: two faces are adjacent iff they share exactly 2 vertices.
	var adj [NumFaces][]int
	for i := 0; i < NumFaces; i++ {
		for j := i + 1; j < NumFaces; j++ {
			shared := 0
			for _, a := range g.faces[i] {
				for _, b := range g.faces[j] {
					if a == b {
						shared++
					}
				}
			}
			if shared == 2 {
				adj[i] = append(adj[i], j)
				adj[j] = append(adj[j], i)
			}
		}
	}
	var matched [NumFaces]bool
	var pairs [][2]int
	var match func() bool
	match = func() bool {
		first := -1
		for f := 0; f < NumFaces; f++ {
			if !matched[f] {
				first = f
				break
			}
		}
		if first < 0 {
			return true
		}
		for _, other := range adj[first] {
			if matched[other] {
				continue
			}
			matched[first], matched[other] = true, true
			pairs = append(pairs, [2]int{first, other})
			if match() {
				return true
			}
			pairs = pairs[:len(pairs)-1]
			matched[first], matched[other] = false, false
		}
		return false
	}
	if !match() || len(pairs) != NumRhombi {
		panic("icosahedron: no perfect face matching")
	}
	for r, p := range pairs {
		f0, f1 := g.faces[p[0]], g.faces[p[1]]
		// Shared vertices become the diagonal endpoints V0 and V2.
		var shared []int
		for _, a := range f0 {
			for _, b := range f1 {
				if a == b {
					shared = append(shared, a)
				}
			}
		}
		if len(shared) != 2 {
			panic("icosahedron: paired faces do not share an edge")
		}
		v0, v2 := shared[0], shared[1]
		v1, v3 := -1, -1
		for _, v := range f0 {
			if v != v0 && v != v2 {
				v1 = v
			}
		}
		for _, v := range f1 {
			if v != v0 && v != v2 {
				v3 = v
			}
		}
		g.rhombusVerts[r] = [4]int{v0, v1, v2, v3}
		g.rhombusFaces[r] = p
		g.faceRhombus[p[0]] = r
		g.faceRhombus[p[1]] = r
	}
}

// buildTransitions pairs up boundary edges across rhombi and records, for
// every (rhombus, edge), the neighboring rhombus edge and orientation.
func (g *Grid) buildTransitions() {
	type edgeRef struct {
		rhombus, edge int
		p, q          int // 3D vertex ids of the directed edge endpoints
	}
	byVerts := map[[2]int][]edgeRef{}
	for r := 0; r < NumRhombi; r++ {
		rv := g.rhombusVerts[r]
		for e := 0; e < 4; e++ {
			p := rv[rhombusEdgeVerts[e][0]]
			q := rv[rhombusEdgeVerts[e][1]]
			key := [2]int{p, q}
			if key[0] > key[1] {
				key[0], key[1] = key[1], key[0]
			}
			byVerts[key] = append(byVerts[key], edgeRef{r, e, p, q})
		}
	}
	for _, refs := range byVerts {
		if len(refs) != 2 {
			panic("icosahedron: boundary edge not shared by exactly two rhombi")
		}
		a, b := refs[0], refs[1]
		// a crosses to b's edge and vice versa.
		flipA := a.p == b.q && a.q == b.p
		flipB := b.p == a.q && b.q == a.p
		if !flipA && !(a.p == b.p && a.q == b.q) {
			panic("icosahedron: inconsistent edge endpoints")
		}
		g.transitions[a.rhombus][a.edge] = edgeTransition{b.rhombus, b.edge, flipA}
		g.transitions[b.rhombus][b.edge] = edgeTransition{a.rhombus, a.edge, flipB}
	}
}
