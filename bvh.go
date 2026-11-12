package taskpkg

import (
	"math"
	"sort"
	"sync/atomic"
)

// maxLeafTris is the maximum number of triangles stored in a leaf node.
const maxLeafTris = 4

// Ray is a ray stamped with the time at which it is evaluated. Triangle
// vertices are linearly interpolated to Time before intersection.
type Ray struct {
	Orig, Dir Vec3
	Time      float64
}

// Hit describes the closest intersection of a ray with the mesh.
type Hit struct {
	T    float64 // ray parameter of the hit
	U, V float64 // barycentric coordinates
	Tri  int     // triangle index
}

// node is one BVH node. Leaves are identified by left == -1 and hold
// triRefs[start:start+count]; internal nodes point at children left/right
// and count is the number of triangles in the whole subtree.
type node struct {
	bounds      AABB
	left, right int32
	start       int32
	count       int32
	rebuildCost float64 // surfaceArea(bounds) * count at the last rebuild
}

func (n *node) isLeaf() bool { return n.left < 0 }

// bvhState is one immutable version of the acceleration structure. It is
// never mutated after being published, so concurrent queries on a Snapshot
// always traverse the exact same structure.
type bvhState struct {
	nodes   []node
	triRefs []int32
	leafOf  []int32 // triangle index -> leaf node id
	root    int32
}

// BVH is a bounding volume hierarchy over a moving triangle mesh. Leaf
// boxes cover the triangle vertices at both ends of the motion interval;
// internal boxes are refit bottom-up on Update, and subtrees whose cost
// (box surface area times leaf count) exceeds the value recorded at their
// last rebuild by more than the configured multiplier are rebuilt locally.
type BVH struct {
	mesh       *Mesh
	multiplier float64
	state      atomic.Pointer[bvhState]
}

// NewBVH builds a BVH over the mesh's current frame. rebuildMultiplier is
// the input cost multiplier: a subtree is rebuilt when its refit cost
// exceeds rebuildMultiplier times the cost recorded at its last rebuild.
// Values <= 1 force a full rebuild on every Update (useful for debugging).
func NewBVH(m *Mesh, rebuildMultiplier float64) *BVH {
	b := &BVH{mesh: m, multiplier: rebuildMultiplier}
	st := &bvhState{root: -1, leafOf: make([]int32, len(m.Tris))}
	if n := len(m.Tris); n > 0 {
		st.triRefs = make([]int32, n)
		for i := range st.triRefs {
			st.triRefs[i] = int32(i)
		}
		st.root = b.buildSubtree(st, m.Frame(), 0, st.triRefs)
		st.fillLeafOf(st.root)
	}
	b.state.Store(st)
	return b
}

// buildSubtree recursively builds nodes over tris (a subslice of
// st.triRefs starting at index start) and returns the new subtree root id.
// Candidates are split at the median along the axis of maximum centroid
// span. New nodes are appended, so existing node ids stay valid.
func (b *BVH) buildSubtree(st *bvhState, f *Frame, start int32, tris []int32) int32 {
	id := int32(len(st.nodes))
	st.nodes = append(st.nodes, node{})

	nd := node{left: -1, right: -1, start: start, count: int32(len(tris))}
	bounds := emptyAABB()
	cBounds := emptyAABB()
	for _, tri := range tris {
		tb := b.mesh.TriBounds(f, int(tri))
		bounds = bounds.Union(tb)
		cBounds = cBounds.Extend(tb.Center())
	}
	nd.bounds = bounds
	nd.rebuildCost = bounds.SurfaceArea() * float64(len(tris))

	if len(tris) > maxLeafTris {
		axis := cBounds.MaxSpanAxis()
		sort.Slice(tris, func(i, j int) bool {
			ci := b.mesh.TriBounds(f, int(tris[i])).Center()
			cj := b.mesh.TriBounds(f, int(tris[j])).Center()
			return ci.Component(axis) < cj.Component(axis)
		})
		mid := len(tris) / 2
		nd.left = b.buildSubtree(st, f, start, tris[:mid])
		nd.right = b.buildSubtree(st, f, start+int32(mid), tris[mid:])
	}
	st.nodes[id] = nd
	return id
}

// Update refits the hierarchy against the mesh's current frame and locally
// rebuilds degraded subtrees. It publishes a new immutable structure
// version; snapshots taken before the call keep traversing the old one.
func (b *BVH) Update() {
	old := b.state.Load()
	f := b.mesh.Frame()
	ns := &bvhState{root: old.root}
	ns.nodes = make([]node, len(old.nodes))
	copy(ns.nodes, old.nodes)
	ns.triRefs = make([]int32, len(old.triRefs))
	copy(ns.triRefs, old.triRefs)

	if ns.root >= 0 {
		ns.root = b.refit(ns, f, ns.root)
	}
	ns.leafOf = make([]int32, len(b.mesh.Tris))
	if ns.root >= 0 {
		ns.fillLeafOf(ns.root)
	}
	b.state.Store(ns)
}

// refit updates bounds bottom-up and rebuilds subtrees whose cost exceeds
// the recorded rebuild cost by more than the multiplier. It returns the id
// of the subtree root, which changes when the subtree is rebuilt.
func (b *BVH) refit(st *bvhState, f *Frame, id int32) int32 {
	n := &st.nodes[id]
	if n.isLeaf() {
		bounds := emptyAABB()
		for i := n.start; i < n.start+n.count; i++ {
			bounds = bounds.Union(b.mesh.TriBounds(f, int(st.triRefs[i])))
		}
		n.bounds = bounds
		return id
	}
	left := b.refit(st, f, n.left)
	right := b.refit(st, f, n.right)
	// Child rebuilds may have grown st.nodes; re-acquire the pointer.
	n = &st.nodes[id]
	n.left, n.right = left, right
	n.bounds = st.nodes[left].bounds.Union(st.nodes[right].bounds)
	if cost := n.bounds.SurfaceArea() * float64(n.count); cost > b.multiplier*n.rebuildCost {
		return b.buildSubtree(st, f, n.start, st.triRefs[n.start:n.start+n.count])
	}
	return id
}

// fillLeafOf records the leaf node id of every triangle so leaf lookups
// stay in sync when rebuilds change node ids.
func (st *bvhState) fillLeafOf(id int32) {
	n := &st.nodes[id]
	if n.isLeaf() {
		for i := n.start; i < n.start+n.count; i++ {
			st.leafOf[st.triRefs[i]] = id
		}
		return
	}
	st.fillLeafOf(n.left)
	st.fillLeafOf(n.right)
}

// Snapshot is a fixed view of one structure version. It is safe for
// concurrent use and unaffected by later Update calls.
type Snapshot struct {
	mesh  *Mesh
	frame *Frame
	st    *bvhState
}

// Snapshot captures the current structure version and mesh frame.
func (b *BVH) Snapshot() *Snapshot {
	return &Snapshot{mesh: b.mesh, frame: b.mesh.Frame(), st: b.state.Load()}
}

// LeafOf returns the id of the leaf node currently holding triangle tri.
func (s *Snapshot) LeafOf(tri int) int32 { return s.st.leafOf[tri] }

// Intersect finds the closest hit of the ray against the mesh, with
// triangle vertices linearly interpolated to ray.Time.
func (s *Snapshot) Intersect(ray Ray) (Hit, bool) {
	st := s.st
	best := Hit{T: math.Inf(1)}
	if st.root < 0 {
		return best, false
	}
	invDir := Vec3{1 / ray.Dir.X, 1 / ray.Dir.Y, 1 / ray.Dir.Z}
	var stack [128]int32
	sp := 0
	stack[sp] = st.root
	sp++
	hit := false
	for sp > 0 {
		sp--
		n := &st.nodes[stack[sp]]
		if !n.bounds.hit(ray.Orig, invDir, best.T) {
			continue
		}
		if n.isLeaf() {
			for i := n.start; i < n.start+n.count; i++ {
				tri := int(st.triRefs[i])
				v0 := s.mesh.Vertex(s.frame, tri, 0, ray.Time)
				v1 := s.mesh.Vertex(s.frame, tri, 1, ray.Time)
				v2 := s.mesh.Vertex(s.frame, tri, 2, ray.Time)
				if t, u, v, ok := intersectTri(ray.Orig, ray.Dir, v0, v1, v2); ok && t < best.T {
					best = Hit{T: t, U: u, V: v, Tri: tri}
					hit = true
				}
			}
			continue
		}
		stack[sp] = n.left
		sp++
		stack[sp] = n.right
		sp++
	}
	return best, hit
}

// intersectTri is a Möller–Trumbore intersection with a determinant
// epsilon, so degenerate (zero-area) triangles never produce hits.
func intersectTri(orig, dir, v0, v1, v2 Vec3) (t, u, v float64, ok bool) {
	e1 := v1.Sub(v0)
	e2 := v2.Sub(v0)
	p := dir.Cross(e2)
	det := e1.Dot(p)
	if math.Abs(det) < 1e-12 {
		return 0, 0, 0, false
	}
	inv := 1 / det
	tv := orig.Sub(v0)
	u = tv.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, 0, 0, false
	}
	q := tv.Cross(e1)
	v = dir.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, 0, 0, false
	}
	t = e2.Dot(q) * inv
	if t < 1e-9 {
		return 0, 0, 0, false
	}
	return t, u, v, true
}
