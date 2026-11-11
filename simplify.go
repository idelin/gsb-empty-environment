package meshsimp

import (
	"container/heap"
	"fmt"
	"math"
)

// RejectReason classifies why a candidate collapse could not be applied.
type RejectReason string

const (
	// RejectNone means the collapse is legal.
	RejectNone RejectReason = ""
	// RejectBoundary means the collapse touches an open boundary and would
	// move or remove boundary geometry.
	RejectBoundary RejectReason = "boundary"
	// RejectNonManifold means the collapse violates the link condition and
	// would create an edge shared by more than two faces.
	RejectNonManifold RejectReason = "non-manifold"
	// RejectNormalFlip means the collapse would invert or degenerate an
	// incident face normal.
	RejectNormalFlip RejectReason = "normal-flip"
	// RejectStale marks heap entries superseded by a later collapse. It is
	// never reported as a termination reason.
	RejectStale RejectReason = "stale"
)

// Options controls Simplify.
type Options struct {
	// TargetFaces is the desired number of triangles.
	TargetFaces int
	// MaxError, when positive, stops simplification once the cheapest
	// remaining collapse exceeds this QEM cost.
	MaxError float64
}

// RejectStat counts how often each rejection rule fired.
type RejectStat struct {
	Boundary     int
	NonManifold  int
	NormalFlip   int
	StaleEntries int
}

// Result describes one simplification run.
type Result struct {
	// Mesh is the simplified mesh, compacted so its indexing is dense.
	Mesh *Mesh
	// FacesCollapsed is the number of successful edge collapses (each
	// removes the one or two faces shared by the collapsed edge).
	FacesCollapsed int
	// ReachedTarget is false when no legal collapse remained before the
	// target face count was achieved.
	ReachedTarget bool
	// StopReason explains why the run ended. It is empty when the target
	// face count was reached.
	StopReason string
	// Rejects counts rejection events by rule.
	Rejects RejectStat
}

// edgeRec tracks a candidate edge and the faces currently sharing it.
type edgeRec struct {
	faces   []int
	version int
	cost    float64
	target  [3]float64
	valid   bool
	reason  RejectReason
}

// heapEntry is one cost-ordered snapshot of an edge. The edge version lets
// the pop loop identify entries made stale by intervening collapses.
type heapEntry struct {
	key     uint64
	cost    float64
	version int
	index   int
}

type edgeHeap []*heapEntry

func (h edgeHeap) Len() int           { return len(h) }
func (h edgeHeap) Less(i, j int) bool { return h[i].cost < h[j].cost }
func (h edgeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}

func (h *edgeHeap) Push(x any) {
	e := x.(*heapEntry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *edgeHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

type simplifier struct {
	mesh *Mesh

	aliveV []bool
	aliveF []bool

	vtoF []map[int]struct{}
	vtoE []map[uint64]struct{}

	edges map[uint64]*edgeRec
	quad  []quadric

	heap *edgeHeap

	collapsed int
	faceCount int
	rejects   RejectStat
}

// Simplify reduces mesh to opts.TargetFaces triangles using QEM edge
// collapses. The input mesh is not modified.
func Simplify(mesh *Mesh, opts Options) (*Result, error) {
	if err := mesh.Validate(); err != nil {
		return nil, err
	}
	if opts.TargetFaces < 0 {
		return nil, fmt.Errorf("meshsimp: TargetFaces must be non-negative, got %d", opts.TargetFaces)
	}

	s := newSimplifier(mesh)
	target := opts.TargetFaces
	if target > len(mesh.Faces) {
		target = len(mesh.Faces)
	}

	var stopReason string
	for s.faceCount > target {
		entry, reason := s.popCheapest()
		if entry == nil {
			stopReason = s.describeStop(reason)
			break
		}
		if opts.MaxError > 0 && entry.cost > opts.MaxError {
			stopReason = fmt.Sprintf(
				"max error %.6g reached at cost %.6g with %d faces (target %d)",
				opts.MaxError, entry.cost, s.faceCount, target)
			heap.Push(s.heap, entry)
			break
		}

		u, v := keyEnds(entry.key)
		// A collapse removes the faces shared by (u,v). Refuse it when that
		// would eliminate every remaining face, since a mesh needs at least
		// one triangle.
		if s.faceCount-len(s.edges[entry.key].faces) < 1 {
			stopReason = fmt.Sprintf(
				"target not reached: stopped at %d faces because the next collapse would remove every remaining triangle",
				s.faceCount)
			break
		}
		s.collapse(u, v, entry)
	}

	out := s.compact()
	return &Result{
		Mesh:           out,
		FacesCollapsed: len(mesh.Faces) - s.faceCount,
		ReachedTarget:  len(out.Faces) <= target,
		StopReason:     stopReason,
		Rejects:        s.rejects,
	}, nil
}

func newSimplifier(mesh *Mesh) *simplifier {
	mesh = mesh.clone()
	nv := len(mesh.Positions)
	nf := len(mesh.Faces)
	s := &simplifier{
		mesh:      mesh,
		aliveV:    make([]bool, nv),
		aliveF:    make([]bool, nf),
		vtoF:      make([]map[int]struct{}, nv),
		vtoE:      make([]map[uint64]struct{}, nv),
		edges:     make(map[uint64]*edgeRec, nf*3/2+1),
		quad:      make([]quadric, nv),
		heap:      &edgeHeap{},
		faceCount: nf,
	}
	for i := range s.aliveV {
		s.aliveV[i] = true
		s.vtoF[i] = make(map[int]struct{})
		s.vtoE[i] = make(map[uint64]struct{})
	}
	for i := range s.aliveF {
		s.aliveF[i] = true
	}

	// One quadric per vertex: sum of the fundamental plane quadrics of every
	// incident face.
	for _, f := range mesh.Faces {
		n := faceNormal(mesh.Positions, f)
		d := -dot(n, mesh.Positions[f[0]])
		q := planeQuadric(n, d)
		for _, vi := range f {
			s.quad[vi].add(q)
		}
	}

	for fi, f := range mesh.Faces {
		for k := 0; k < 3; k++ {
			a, b := f[k], f[(k+1)%3]
			key := edgeKey(a, b)
			rec, ok := s.edges[key]
			if !ok {
				rec = &edgeRec{}
				s.edges[key] = rec
				s.vtoE[a][key] = struct{}{}
				s.vtoE[b][key] = struct{}{}
			}
			rec.faces = append(rec.faces, fi)
		}
		s.vtoF[f[0]][fi] = struct{}{}
		s.vtoF[f[1]][fi] = struct{}{}
		s.vtoF[f[2]][fi] = struct{}{}
	}

	for key, rec := range s.edges {
		s.evaluate(key, rec)
		heap.Push(s.heap, &heapEntry{key: key, cost: rec.cost, version: rec.version})
	}
	heap.Init(s.heap)
	return s
}

// evaluate recomputes cost, optimal target position and legality for an edge.
func (s *simplifier) evaluate(key uint64, rec *edgeRec) {
	u, v := keyEnds(key)
	rec.version++
	rec.valid = false

	if reason := s.checkCollapse(u, v); reason != RejectNone {
		rec.reason = reason
		rec.cost = math.Inf(1)
		return
	}

	q := s.quad[u]
	q.add(s.quad[v])
	target := s.previewTarget(u, v)
	cost := q.errorAt(target)
	if cost < 0 {
		cost = 0
	}
	rec.cost = cost
	rec.target = target
	rec.reason = RejectNone
	rec.valid = true
}

// checkCollapse applies the three topology/geometry rejection rules.
func (s *simplifier) checkCollapse(u, v int) RejectReason {
	// Rule 1: open boundaries are immutable. Any edge touching a boundary
	// vertex is refused, so boundary vertices and boundary edges never move
	// or disappear.
	if s.isBoundaryVertex(u) || s.isBoundaryVertex(v) {
		return RejectBoundary
	}

	// Rule 2: link condition. An edge (u,v) is collapsible only when u and v
	// share exactly the vertices closing its incident faces: two common
	// neighbors for an interior edge, one for a boundary edge.
	common := s.commonNeighbors(u, v)
	want := 2
	if rec := s.edges[edgeKey(u, v)]; rec != nil && len(rec.faces) == 1 {
		want = 1
	}
	if len(common) != want {
		return RejectNonManifold
	}

	// Rule 3: no incident face may flip its normal or become degenerate.
	if s.flipsNormal(u, v) {
		return RejectNormalFlip
	}
	return RejectNone
}

func (s *simplifier) isBoundaryVertex(v int) bool {
	for key := range s.vtoE[v] {
		rec, ok := s.edges[key]
		if !ok {
			delete(s.vtoE[v], key)
			continue
		}
		if len(rec.faces) == 1 {
			return true
		}
	}
	return false
}

// commonNeighbors returns vertices adjacent to both u and v through a live
// edge.
func (s *simplifier) commonNeighbors(u, v int) map[int]struct{} {
	common := make(map[int]struct{})
	neighbors := func(x int) map[int]struct{} {
		out := make(map[int]struct{})
		for key := range s.vtoE[x] {
			rec, ok := s.edges[key]
			if !ok || len(rec.faces) == 0 {
				continue
			}
			a, b := keyEnds(key)
			if a == x {
				out[b] = struct{}{}
			} else {
				out[a] = struct{}{}
			}
		}
		return out
	}
	for neighbor := range neighbors(u) {
		if neighbor == v {
			continue
		}
		if _, ok := neighbors(v)[neighbor]; ok {
			common[neighbor] = struct{}{}
		}
	}
	return common
}

// flipsNormal checks every face incident to u or v other than the faces that
// die with the edge. A face flips when the moved vertex reverses its winding
// normal, and a zero-area result is treated as a flip.
func (s *simplifier) flipsNormal(u, v int) bool {
	dying := make(map[int]struct{}, 2)
	for _, fi := range s.edges[edgeKey(u, v)].faces {
		dying[fi] = struct{}{}
	}

	target := s.previewTarget(u, v)
	incident := make(map[int]struct{})
	for fi := range s.vtoF[u] {
		incident[fi] = struct{}{}
	}
	for fi := range s.vtoF[v] {
		incident[fi] = struct{}{}
	}

	positions := s.mesh.Positions
	for fi := range incident {
		if _, dead := dying[fi]; dead {
			continue
		}
		f := s.mesh.Faces[fi]
		before := faceNormal(positions, f)

		// Build the post-collapse triangle position by position: u and v
		// both map onto target, every other vertex keeps its position.
		preview := [3][3]float64{}
		for k, vi := range f {
			if vi == u || vi == v {
				preview[k] = target
			} else {
				preview[k] = positions[vi]
			}
		}
		after := cross(sub(preview[1], preview[0]), sub(preview[2], preview[0]))
		// Compare the signed normal change. Using the raw dot product avoids
		// normalizing a near-degenerate cross product, and a non-positive sign
		// catches both inverted normals and zero-area collapses.
		if norm(after) <= 1e-12 || dot(before, after) <= 0 {
			return true
		}
	}
	return false
}

// previewTarget recomputes the candidate target position without mutating the
// edge record.
func (s *simplifier) previewTarget(u, v int) [3]float64 {
	q := s.quad[u]
	q.add(s.quad[v])
	return bestPosition(q, s.mesh.Positions[u], s.mesh.Positions[v])
}

// popCheapest returns the lowest-cost live, current heap entry. Entries that
// are stale or have since become illegal are skipped and counted.
func (s *simplifier) popCheapest() (*heapEntry, RejectReason) {
	var lastReason RejectReason
	for s.heap.Len() > 0 {
		entry := heap.Pop(s.heap).(*heapEntry)
		rec, ok := s.edges[entry.key]
		if !ok {
			s.rejects.StaleEntries++
			continue
		}
		a, b := keyEnds(entry.key)
		if !s.aliveV[a] || !s.aliveV[b] {
			s.rejects.StaleEntries++
			continue
		}
		if entry.version != rec.version {
			s.rejects.StaleEntries++
			continue
		}
		// The record may have become illegal since it was evaluated because a
		// neighboring collapse changed its topology without touching its
		// version. Re-validate against the live mesh right before use.
		if reason := s.checkCollapse(a, b); reason != RejectNone {
			s.countReject(reason)
			rec.valid = false
			rec.reason = reason
			rec.version++
			lastReason = reason
			continue
		}
		if !rec.valid {
			if rec.reason != RejectNone && rec.reason != RejectStale {
				s.countReject(rec.reason)
			}
			lastReason = rec.reason
			continue
		}
		return entry, RejectNone
	}
	return nil, lastReason
}

func (s *simplifier) countReject(reason RejectReason) {
	switch reason {
	case RejectBoundary:
		s.rejects.Boundary++
	case RejectNonManifold:
		s.rejects.NonManifold++
	case RejectNormalFlip:
		s.rejects.NormalFlip++
	}
}

// compact produces a dense copy containing only live vertices and faces.
func (s *simplifier) compact() *Mesh {
	mesh := s.mesh

	// A vertex can be alive yet unreferenced (its last incident faces died
	// while it served as a collapse endpoint). Only keep vertices that still
	// belong to a live face.
	used := make([]bool, len(mesh.Positions))
	for fi, alive := range s.aliveF {
		if !alive {
			continue
		}
		for _, vi := range mesh.Faces[fi] {
			used[vi] = true
		}
	}

	remap := make([]int, len(mesh.Positions))
	for i := range remap {
		remap[i] = -1
	}

	out := &Mesh{NormalizeAttributes: mesh.NormalizeAttributes}
	for vi, alive := range s.aliveV {
		if !alive || !used[vi] {
			continue
		}
		remap[vi] = len(out.Positions)
		out.Positions = append(out.Positions, mesh.Positions[vi])
	}
	if mesh.Normals != nil {
		out.Normals = make([][3]float64, 0, len(out.Positions))
	}
	if mesh.UVs != nil {
		out.UVs = make([][2]float64, 0, len(out.Positions))
	}
	if mesh.Colors != nil {
		out.Colors = make([][3]float64, 0, len(out.Positions))
	}
	for vi, alive := range s.aliveV {
		if !alive || !used[vi] {
			continue
		}
		if mesh.Normals != nil {
			out.Normals = append(out.Normals, mesh.Normals[vi])
		}
		if mesh.UVs != nil {
			out.UVs = append(out.UVs, mesh.UVs[vi])
		}
		if mesh.Colors != nil {
			out.Colors = append(out.Colors, mesh.Colors[vi])
		}
	}

	for fi, alive := range s.aliveF {
		if !alive {
			continue
		}
		f := mesh.Faces[fi]
		out.Faces = append(out.Faces, [3]int{
			remap[f[0]], remap[f[1]], remap[f[2]],
		})
	}
	return out
}

// describeStop turns the final rejection state into an actionable reason
// string explaining why the target face count could not be reached.
func (s *simplifier) describeStop(last RejectReason) string {
	faces := len(s.mesh.Faces) - s.collapsed
	switch last {
	case RejectBoundary:
		return fmt.Sprintf(
			"target not reached: stopped at %d faces because all remaining edges touch an open boundary and collapsing them would change it",
			faces)
	case RejectNonManifold:
		return fmt.Sprintf(
			"target not reached: stopped at %d faces because every remaining edge collapse would create a non-manifold edge",
			faces)
	case RejectNormalFlip:
		return fmt.Sprintf(
			"target not reached: stopped at %d faces because every remaining edge collapse would flip or degenerate a face normal",
			faces)
	default:
		return fmt.Sprintf(
			"target not reached: stopped at %d faces because no collapsible edge remains",
			faces)
	}
}

// interpolationWeights splits a target point on the u-v segment into convex
// weights. When the endpoints coincide the midpoint is used.
func interpolationWeights(u, v, target [3]float64) (wU, wV float64) {
	seg := sub(v, u)
	lengthSq := dot(seg, seg)
	if lengthSq == 0 {
		return 0.5, 0.5
	}
	t := dot(sub(target, u), seg) / lengthSq
	t = clamp01(t)
	return 1 - t, t
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func blendVec3(attr [][3]float64, u, v int, wU, wV float64, renorm bool) [3]float64 {
	if attr == nil {
		return [3]float64{}
	}
	a, b := attr[u], attr[v]
	out := [3]float64{
		a[0]*wU + b[0]*wV,
		a[1]*wU + b[1]*wV,
		a[2]*wU + b[2]*wV,
	}
	if renorm {
		out = normalize(out)
	}
	return out
}

func blendUV(attr [][2]float64, u, v int, wU, wV float64) [2]float64 {
	if attr == nil {
		return [2]float64{}
	}
	a, b := attr[u], attr[v]
	return [2]float64{
		a[0]*wU + b[0]*wV,
		a[1]*wU + b[1]*wV,
	}
}
