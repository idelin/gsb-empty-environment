package taskpkg

import (
	"fmt"
	"math/big"
)

// Intersection records a vertex generated on an original mesh edge. EdgeID is
// stable for the input mesh: edges are numbered in first-seen triangle order.
// All triangles incident to the same original edge share exactly one Vertex.
// T is measured from Edge.A toward Edge.B.
type Intersection struct {
	EdgeID int
	Edge   Edge
	Vertex int
	T      float64
	Point  Vec3
}

// Loop is one oriented boundary loop of a cap, expressed as indices into
// Result.Vertices. Loops are not explicitly closed: the last vertex is
// connected back to the first.
//
// Area is signed in a 2D basis whose normal points outward from the retained
// solid (that is, opposite the cutting plane normal). Outer loops have
// positive area and holes have negative area. Parent is -1 for an outer loop
// and otherwise is the index of its immediately containing loop. Depth is the
// nesting depth (0 for outer loops).
type Loop struct {
	Vertices []int
	Area     float64
	Parent   int
	Depth    int
}

// Result is the clipped surface together with cap diagnostics.
type Result struct {
	Mesh
	// CapTriangles contains copies of the generated cap triangles.
	CapTriangles []Triangle
	// CapIndices contains the indices of those triangles in Mesh.Triangles.
	CapIndices []int
	// Loops contains the boundary loops used to triangulate the caps.
	Loops []Loop
	// Intersections contains the edge-keyed vertices created on cut edges.
	Intersections []Intersection
	// CoplanarRetained and CoplanarDropped count input triangles that were
	// exactly coplanar with the plane and kept or dropped according to normal.
	CoplanarRetained int
	CoplanarDropped  int
}

// Cut cuts m by p and retains the half-space p(x,y,z) >= 0. The input must be
// a finite, closed, consistently oriented triangular 2-manifold.
//
// Triangles crossing the plane are clipped, with each new intersection vertex
// shared by original edge identity. The exposed boundary of the kept surface
// is assembled into oriented loops and triangulated in the plane, including
// holes. A coplanar input triangle is retained only when its outward normal
// points opposite the plane normal; such a triangle already caps the cut and
// is never duplicated by generated cap geometry.
func Cut(m Mesh, p Plane) (*Result, error) {
	return cutWithPlane(m, p, ratPlaneFrom(p))
}

// CutExact is Cut with an explicitly rational plane.
func CutExact(m Mesh, p *ExactPlane) (*Result, error) {
	if p == nil {
		return nil, ErrDegeneratePlane
	}
	approx := p.Plane()
	if err := approx.validate(); err != nil {
		return nil, err
	}
	return cutWithPlane(m, approx, ratPlane{p.A, p.B, p.C, p.D})
}

// CutMesh is a convenience wrapper returning only the renderable output mesh.
func CutMesh(m Mesh, p Plane) (Mesh, error) {
	r, err := Cut(m, p)
	if err != nil {
		return Mesh{}, err
	}
	return r.Mesh, nil
}

// Slice is an alias for Cut.
func Slice(m Mesh, p Plane) (*Result, error) { return Cut(m, p) }

// ClipMesh is an alias for CutMesh.
func ClipMesh(m Mesh, p Plane) (Mesh, error) { return CutMesh(m, p) }

// Cut cuts m with p. It is the method form of the package-level Cut function.
func (m Mesh) Cut(p Plane) (*Result, error) { return Cut(m, p) }

type clipRef struct {
	id   int
	side int
}

type cutter struct {
	m        Mesh
	plane    Plane
	rp       ratPlane
	ratVerts []ratVec
	sides    []int
	b        *meshBuilder

	surface   []Triangle
	coplanarK int
	coplanarD int
}

type meshBuilder struct {
	verts     []Vec3
	exact     []ratVec
	onPlane   []bool
	originals map[int]int
	edges     map[Edge]int
	edgeIDs   map[Edge]int
	inters    []Intersection
}

func newMeshBuilder(edgeIDs map[Edge]int) *meshBuilder {
	return &meshBuilder{
		originals: make(map[int]int),
		edges:     make(map[Edge]int),
		edgeIDs:   edgeIDs,
	}
}

func (b *meshBuilder) addVertex(v Vec3, e ratVec, onPlane bool) int {
	id := len(b.verts)
	b.verts = append(b.verts, v)
	b.exact = append(b.exact, e)
	b.onPlane = append(b.onPlane, onPlane)
	return id
}

func (b *meshBuilder) original(id int, v Vec3, e ratVec, onPlane bool) int {
	if out, ok := b.originals[id]; ok {
		return out
	}
	out := b.addVertex(v, e, onPlane)
	b.originals[id] = out
	return out
}

func (b *meshBuilder) edgePoint(key Edge, point ratVec, t *big.Rat) int {
	if out, ok := b.edges[key]; ok {
		return out
	}
	out := b.addVertex(vecFromRat(point), point, true)
	b.edges[key] = out
	id, ok := b.edgeIDs[key]
	if !ok {
		id = -1
	}
	b.inters = append(b.inters, Intersection{
		EdgeID: id,
		Edge:   key,
		Vertex: out,
		T:      ratToFloat(t),
		Point:  vecFromRat(point),
	})
	return out
}

func cutWithPlane(m Mesh, p Plane, rp ratPlane) (*Result, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}

	edgeIDs := make(map[Edge]int)
	nextEdge := 0
	for _, t := range m.Triangles {
		for _, e := range t.edges() {
			key := edgeOf(e[0], e[1])
			if _, ok := edgeIDs[key]; !ok {
				edgeIDs[key] = nextEdge
				nextEdge++
			}
		}
	}

	c := &cutter{
		m:     m,
		plane: p,
		rp:    rp,
		b:     newMeshBuilder(edgeIDs),
	}
	c.ratVerts = make([]ratVec, len(m.Vertices))
	c.sides = make([]int, len(m.Vertices))
	for i, v := range m.Vertices {
		c.ratVerts[i] = ratVecFrom(v)
		c.sides[i] = c.rp.eval(c.ratVerts[i]).Sign()
	}

	for _, t := range m.Triangles {
		if err := c.clipTriangle(t); err != nil {
			return nil, err
		}
	}

	frame, err := newPlaneFrame(p)
	if err != nil {
		return nil, err
	}
	loops, err := c.buildCapLoops(frame)
	if err != nil {
		return nil, err
	}
	capTris, err := triangulateCap(loops, c.b.verts, frame)
	if err != nil {
		return nil, err
	}

	all := make([]Triangle, 0, len(c.surface)+len(capTris))
	all = append(all, c.surface...)
	capStart := len(all)
	all = append(all, capTris...)

	mesh, remap, err := compactMesh(c.b.verts, all, c.b.onPlane)
	if err != nil {
		return nil, err
	}
	if len(mesh.Triangles) > 0 {
		if err := mesh.Validate(); err != nil {
			return nil, fmt.Errorf("taskpkg: generated cut is not manifold: %w", err)
		}
	}

	// Remap diagnostics after vertex compaction.
	for i := range c.b.inters {
		c.b.inters[i].Vertex = remap[c.b.inters[i].Vertex]
	}
	for i := range loops {
		loops[i].Vertices = remapLoop(loops[i].Vertices, remap)
	}
	capTriangles := make([]Triangle, len(capTris))
	capIndices := make([]int, len(capTris))
	for i, t := range capTris {
		capTriangles[i] = remapTriangle(t, remap)
		capIndices[i] = capStart + i
	}

	return &Result{
		Mesh:             mesh,
		CapTriangles:     capTriangles,
		CapIndices:       capIndices,
		Loops:            loops,
		Intersections:    c.b.inters,
		CoplanarRetained: c.coplanarK,
		CoplanarDropped:  c.coplanarD,
	}, nil
}

func (c *cutter) outOriginal(id int) clipRef {
	out := c.b.original(id, c.m.Vertices[id], c.ratVerts[id], c.sides[id] == 0)
	return clipRef{id: out, side: c.sides[id]}
}

func (c *cutter) intersection(a, b int) clipRef {
	key := edgeOf(a, b)
	ra, rb := c.ratVerts[a], c.ratVerts[b]
	fa, fb := c.rp.eval(ra), c.rp.eval(rb)
	// t = -fa/(fb-fa) = fa/(fa-fb), with point = A + t*(B-A).
	den := ratSub(fa, fb)
	t := new(big.Rat).Quo(fa, den)
	d := rb.sub(ra)
	point := ratVec{
		x: new(big.Rat).Add(ra.x, ratMul(t, d.x)),
		y: new(big.Rat).Add(ra.y, ratMul(t, d.y)),
		z: new(big.Rat).Add(ra.z, ratMul(t, d.z)),
	}
	out := c.b.edgePoint(key, point, t)
	return clipRef{id: out, side: 0}
}

func (c *cutter) clipTriangle(t Triangle) error {
	sides := [3]int{c.sides[t[0]], c.sides[t[1]], c.sides[t[2]]}
	coplanar := sides[0] == 0 && sides[1] == 0 && sides[2] == 0
	if coplanar {
		// The outward normal decides whether this triangle is already the cap
		// of the retained solid or a back face that must be removed.
		a := c.ratVerts[t[0]]
		normal := ratCross(c.ratVerts[t[1]].sub(a), c.ratVerts[t[2]].sub(a))
		planeNormal := ratVec{x: c.rp.a, y: c.rp.b, z: c.rp.c}
		if ratDot(normal, planeNormal).Sign() < 0 {
			refs := [3]clipRef{c.outOriginal(t[0]), c.outOriginal(t[1]), c.outOriginal(t[2])}
			c.addSurface(Triangle{refs[0].id, refs[1].id, refs[2].id})
			c.coplanarK++
		} else {
			c.coplanarD++
		}
		return nil
	}

	allNonNegative := true
	allNonPositive := true
	for _, s := range sides {
		if s < 0 {
			allNonNegative = false
		}
		if s > 0 {
			allNonPositive = false
		}
	}
	if allNonNegative {
		refs := [3]clipRef{c.outOriginal(t[0]), c.outOriginal(t[1]), c.outOriginal(t[2])}
		c.addSurface(Triangle{refs[0].id, refs[1].id, refs[2].id})
		return nil
	}
	if allNonPositive {
		return nil
	}

	var poly []clipRef
	for i := 0; i < 3; i++ {
		cur := c.outOriginal(t[i])
		nxtIdx := (i + 1) % 3
		nxt := c.outOriginal(t[nxtIdx])
		if cur.side >= 0 {
			poly = appendRef(poly, cur)
		}
		if cur.side*nxt.side < 0 {
			poly = appendRef(poly, c.intersection(t[i], t[nxtIdx]))
		}
	}
	if len(poly) < 3 {
		return nil
	}
	for i := 1; i+1 < len(poly); i++ {
		tri := Triangle{poly[0].id, poly[i].id, poly[i+1].id}
		if c.nonzeroTriangle(tri) {
			c.addSurface(tri)
		}
	}
	return nil
}

func appendRef(poly []clipRef, r clipRef) []clipRef {
	if len(poly) > 0 && poly[len(poly)-1].id == r.id {
		return poly
	}
	if len(poly) > 1 && poly[0].id == r.id {
		return poly
	}
	return append(poly, r)
}

func (c *cutter) nonzeroTriangle(t Triangle) bool {
	a := c.b.exact[t[0]]
	b := c.b.exact[t[1]]
	cross := ratCross(b.sub(a), c.b.exact[t[2]].sub(a))
	return cross.x.Sign() != 0 || cross.y.Sign() != 0 || cross.z.Sign() != 0
}

func (c *cutter) addSurface(t Triangle) {
	if t[0] == t[1] || t[1] == t[2] || t[2] == t[0] {
		return
	}
	c.surface = append(c.surface, t)
}

func compactMesh(verts []Vec3, tris []Triangle, onPlane []bool) (Mesh, map[int]int, error) {
	remap := make(map[int]int)
	outVerts := make([]Vec3, 0, len(verts))
	for _, t := range tris {
		for _, id := range t {
			if id < 0 || id >= len(verts) {
				return Mesh{}, nil, fmt.Errorf("taskpkg: internal vertex index %d out of range", id)
			}
			if _, ok := remap[id]; !ok {
				remap[id] = len(outVerts)
				outVerts = append(outVerts, verts[id])
			}
		}
	}
	outTris := make([]Triangle, len(tris))
	for i, t := range tris {
		outTris[i] = remapTriangle(t, remap)
	}
	return Mesh{Vertices: outVerts, Triangles: outTris}, remap, nil
}

func remapTriangle(t Triangle, remap map[int]int) Triangle {
	return Triangle{remap[t[0]], remap[t[1]], remap[t[2]]}
}

func remapLoop(ids []int, remap map[int]int) []int {
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = remap[id]
	}
	return out
}

type planeFrame struct {
	u Vec3 // u × v = cap normal (-plane normal)
	v Vec3
}

func newPlaneFrame(p Plane) (planeFrame, error) {
	n, ok := p.Normal().Unit()
	if !ok {
		return planeFrame{}, ErrDegeneratePlane
	}
	m := n.Scale(-1)
	helper := Vec3{1, 0, 0}
	if abs(m.X) > 0.9 {
		helper = Vec3{0, 1, 0}
	}
	u, ok := helper.Cross(m).Unit()
	if !ok {
		return planeFrame{}, ErrDegeneratePlane
	}
	v := m.Cross(u)
	return planeFrame{u: u, v: v}, nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
