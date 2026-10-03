package hexgrid

import "math"

var sqrt3 = math.Sqrt(3)

const numFaces = 20

// face is one oriented triangle of the icosahedron together with a local
// 2D orthonormal frame (u, v) and the plane normal w, precomputed for
// gnomonic projection and barycentric containment tests.
type face struct {
	verts [3]int
	a     vec3 // plane origin (first vertex)
	u     vec3 // in-plane unit axis x
	v     vec3 // in-plane unit axis y
	w     vec3 // outward unit normal
	e1    vec3 // b - a
	e2    vec3 // c - a
	d11   float64
	d12   float64
	d22   float64
	det   float64
	aw    float64 // a . w (plane offset)
	adj   [3]int  // adj[i] = face across the edge opposite verts[i]
}

var faces [numFaces]face

// icosaVertices returns the 12 vertices of an icosahedron with edge length 2.
func icosaVertices() [12]vec3 {
	p := (1 + math.Sqrt(5)) / 2
	return [12]vec3{
		{0, 1, p}, {0, 1, -p}, {0, -1, p}, {0, -1, -p},
		{1, p, 0}, {1, -p, 0}, {-1, p, 0}, {-1, -p, 0},
		{p, 0, 1}, {p, 0, -1}, {-p, 0, 1}, {-p, 0, -1},
	}
}

func init() {
	verts := icosaVertices()

	// Edges connect vertices at distance ~2.
	var edge [12][12]bool
	for i := 0; i < 12; i++ {
		for j := i + 1; j < 12; j++ {
			d := verts[i].sub(verts[j]).norm()
			if d > 1.99 && d < 2.01 {
				edge[i][j] = true
				edge[j][i] = true
			}
		}
	}

	// Faces are triples of mutually connected vertices, oriented outward.
	fi := 0
	for i := 0; i < 12 && fi < numFaces; i++ {
		for j := i + 1; j < 12 && fi < numFaces; j++ {
			if !edge[i][j] {
				continue
			}
			for k := j + 1; k < 12 && fi < numFaces; k++ {
				if !edge[i][k] || !edge[j][k] {
					continue
				}
				a := verts[i]
				b := verts[j]
				c := verts[k]
				vi, vj, vk := i, j, k
				e1 := b.sub(a)
				e2 := c.sub(a)
				w := e1.cross(e2)
				if w.dot(a.add(b).add(c)) < 0 {
					b, c = c, b
					vj, vk = vk, vj
					e1 = b.sub(a)
					e2 = c.sub(a)
					w = e1.cross(e2)
				}
				u := e1.normalized()
				wn := w.normalized()
				faces[fi] = face{
					verts: [3]int{vi, vj, vk},
					a:     a,
					u:     u,
					v:     wn.cross(u),
					w:     wn,
					e1:    e1,
					e2:    e2,
					d11:   e1.dot(e1),
					d12:   e1.dot(e2),
					d22:   e2.dot(e2),
					det:   e1.dot(e1)*e2.dot(e2) - e1.dot(e2)*e1.dot(e2),
					aw:    a.dot(wn),
					adj:   [3]int{-1, -1, -1},
				}
				fi++
			}
		}
	}
	if fi != numFaces {
		panic("hexgrid: failed to build icosahedron")
	}

	// Adjacency: two faces are neighbors iff they share exactly 2 vertices.
	for i := 0; i < numFaces; i++ {
		for j := i + 1; j < numFaces; j++ {
			shared := make(map[int]bool, 3)
			cnt := 0
			for _, va := range faces[i].verts {
				for _, vb := range faces[j].verts {
					if va == vb {
						shared[va] = true
						cnt++
					}
				}
			}
			if cnt != 2 {
				continue
			}
			for m := 0; m < 3; m++ {
				if !shared[faces[i].verts[m]] {
					faces[i].adj[m] = j
				}
				if !shared[faces[j].verts[m]] {
					faces[j].adj[m] = i
				}
			}
		}
	}
}

const baryEps = 1e-9

// bary returns the barycentric coordinates (alpha, beta) of p with respect
// to the face triangle: p = a + alpha*(b-a) + beta*(c-a).
func (f *face) bary(p vec3) (alpha, beta float64) {
	d := p.sub(f.a)
	d1 := d.dot(f.e1)
	d2 := d.dot(f.e2)
	alpha = (f.d22*d1 - f.d12*d2) / f.det
	beta = (f.d11*d2 - f.d12*d1) / f.det
	return alpha, beta
}

func baryInside(alpha, beta float64) bool {
	return alpha >= -baryEps && beta >= -baryEps && alpha+beta <= 1+baryEps
}

// faceContaining returns the index of the face whose triangle contains the
// ray from the origin through s (gnomonic projection).
func faceContaining(s vec3) int {
	best := 0
	bestDot := -math.MaxFloat64
	for i := range faces {
		f := &faces[i]
		sw := s.dot(f.w)
		if sw > bestDot {
			bestDot = sw
			best = i
		}
		if sw <= 0 {
			continue
		}
		p := s.scale(f.aw / sw)
		al, be := f.bary(p)
		if baryInside(al, be) {
			return i
		}
	}
	return best
}
