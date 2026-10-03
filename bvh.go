// Package spacetime implements a 4D (3D space + 1D time) BVH tree indexer
// for moving-object trajectories.
//
// Each trajectory is decomposed into linear segments. A segment occupies an
// axis-aligned bounding box in 4D space (x, y, z, t). The BVH accelerates
// spatiotemporal window queries such as:
//
//	"all trajectory legs that entered cube [x,y,z]..[X,Y,Z]
//	 between 14:00 and 14:10 above 100m altitude"
//
// Query returns candidates pruned by 4D AABB intersection; QueryExact
// additionally clips each candidate segment (a 4D line segment) against the
// query box with the slab method and reports the exact entry/exit times and
// positions of the portion inside the window.
package spacetime

import (
	"math"
	"sort"
)

// Dim is the dimension of the index: 3 spatial axes + 1 time axis.
const Dim = 4

// Vec3 is a position in 3D space (meters).
type Vec3 struct {
	X, Y, Z float64
}

// Segment is one leg of a trajectory: linear motion from Start at time T0
// to End at time T1 (times in seconds, e.g. seconds since midnight).
type Segment struct {
	TrajectoryID string  // identifier of the moving object / track
	Index        int     // ordinal of this leg inside its trajectory
	Start, End   Vec3    // endpoints in 3D space
	T0, T1       float64 // timestamps of the endpoints (seconds)
}

// Bounds returns the 4D AABB (x, y, z, t) enclosing the segment.
func (s Segment) Bounds() AABB4 {
	return AABB4{
		Min: [Dim]float64{
			math.Min(s.Start.X, s.End.X),
			math.Min(s.Start.Y, s.End.Y),
			math.Min(s.Start.Z, s.End.Z),
			math.Min(s.T0, s.T1),
		},
		Max: [Dim]float64{
			math.Max(s.Start.X, s.End.X),
			math.Max(s.Start.Y, s.End.Y),
			math.Max(s.Start.Z, s.End.Z),
			math.Max(s.T0, s.T1),
		},
	}
}

// AABB4 is an axis-aligned bounding box in (x, y, z, t).
type AABB4 struct {
	Min, Max [Dim]float64
}

// Box builds a 4D AABB from spatial corners and a time range.
func Box(min, max Vec3, t0, t1 float64) AABB4 {
	return AABB4{
		Min: [Dim]float64{min.X, min.Y, min.Z, t0},
		Max: [Dim]float64{max.X, max.Y, max.Z, t1},
	}
}

// Intersects reports whether two 4D boxes overlap (inclusive boundaries).
func (b AABB4) Intersects(o AABB4) bool {
	for d := 0; d < Dim; d++ {
		if b.Max[d] < o.Min[d] || o.Max[d] < b.Min[d] {
			return false
		}
	}
	return true
}

// Union returns the smallest box containing both b and o.
func (b AABB4) Union(o AABB4) AABB4 {
	var u AABB4
	for d := 0; d < Dim; d++ {
		u.Min[d] = math.Min(b.Min[d], o.Min[d])
		u.Max[d] = math.Max(b.Max[d], o.Max[d])
	}
	return u
}

// volume is the 4D hyper-volume, used only as a split/insertion heuristic.
func (b AABB4) volume() float64 {
	v := 1.0
	for d := 0; d < Dim; d++ {
		v *= b.Max[d] - b.Min[d]
	}
	return v
}

// enlargement is the extra volume needed to extend b to contain o.
func enlargement(b, o AABB4) float64 {
	return b.Union(o).volume() - b.volume()
}

// longestAxis returns the axis with the greatest extent.
func (b AABB4) longestAxis() int {
	axis, widest := 0, -1.0
	for d := 0; d < Dim; d++ {
		if e := b.Max[d] - b.Min[d]; e > widest {
			axis, widest = d, e
		}
	}
	return axis
}

// node is an internal BVH node or a leaf holding segments.
type node struct {
	bounds      AABB4
	leaf        bool
	left, right *node
	segments    []Segment // only used on leaves
}

// BVH is a dynamic 4D bounding-volume hierarchy over trajectory segments.
// It is not safe for concurrent use.
type BVH struct {
	root        *node
	maxLeafSize int
	size        int
	stats       Stats
}

// Stats reports traversal work performed by the most recent Query or
// QueryExact call, useful for diagnostics and tests.
type Stats struct {
	NodesVisited    int // tree nodes whose bounds were tested
	LeavesVisited   int // leaf nodes reached
	SegmentsScanned int // individual segments examined in leaves
}

// LastQueryStats returns traversal counters of the last query.
func (t *BVH) LastQueryStats() Stats { return t.stats }

// New creates an empty BVH. maxLeafSize is the maximum number of segments
// stored in a leaf before it splits (clamped to >= 4).
func New(maxLeafSize int) *BVH {
	if maxLeafSize < 4 {
		maxLeafSize = 4
	}
	return &BVH{maxLeafSize: maxLeafSize}
}

// Size returns the number of indexed segments.
func (t *BVH) Size() int { return t.size }

// Insert adds one trajectory segment to the index.
func (t *BVH) Insert(seg Segment) {
	b := seg.Bounds()
	if t.root == nil {
		t.root = &node{leaf: true, bounds: b, segments: []Segment{seg}}
	} else {
		t.insert(t.root, seg, b)
	}
	t.size++
}

// InsertTrajectory decomposes a polyline trajectory into segments and
// inserts all of them. pts[i] is the position at times[i]; the slices must
// have equal length >= 2 and times must be non-decreasing.
func (t *BVH) InsertTrajectory(id string, pts []Vec3, times []float64) int {
	if len(pts) != len(times) || len(pts) < 2 {
		return 0
	}
	n := 0
	for i := 0; i+1 < len(pts); i++ {
		t.Insert(Segment{
			TrajectoryID: id,
			Index:        i,
			Start:        pts[i],
			End:          pts[i+1],
			T0:           times[i],
			T1:           times[i+1],
		})
		n++
	}
	return n
}

func (t *BVH) insert(n *node, seg Segment, b AABB4) {
	n.bounds = n.bounds.Union(b)
	if n.leaf {
		n.segments = append(n.segments, seg)
		if len(n.segments) > t.maxLeafSize {
			t.split(n)
		}
		return
	}
	// Descend into the child whose bounds grow the least.
	el, er := enlargement(n.left.bounds, b), enlargement(n.right.bounds, b)
	if el < er || (el == er && n.left.bounds.volume() <= n.right.bounds.volume()) {
		t.insert(n.left, seg, b)
	} else {
		t.insert(n.right, seg, b)
	}
}

// Build replaces the tree contents with a bulk-loaded index over segs.
// Bulk loading (top-down recursive median split) produces a balanced,
// low-overlap tree with noticeably faster queries than incremental
// insertion; prefer it when all segments are known up front.
func (t *BVH) Build(segs []Segment) {
	items := make([]buildItem, len(segs))
	for i, s := range segs {
		items[i] = buildItem{seg: s, center: centerOf(s)}
	}
	t.root = buildRecursive(items, t.maxLeafSize)
	t.size = len(segs)
}

// Rebuild re-balances the current contents in place. Useful after a large
// batch of incremental Insert calls.
func (t *BVH) Rebuild() {
	var all []Segment
	collect(t.root, &all)
	t.Build(all)
}

func collect(n *node, out *[]Segment) {
	if n == nil {
		return
	}
	if n.leaf {
		*out = append(*out, n.segments...)
		return
	}
	collect(n.left, out)
	collect(n.right, out)
}

// buildItem pairs a segment with its precomputed 4D center so the
// recursive median splits never recompute bounds inside comparators.
type buildItem struct {
	seg    Segment
	center [Dim]float64
}

func buildRecursive(items []buildItem, maxLeafSize int) *node {
	if len(items) == 0 {
		return nil
	}
	bounds := boundsOfItems(items)
	if len(items) <= maxLeafSize {
		segs := make([]Segment, len(items))
		for i, it := range items {
			segs[i] = it.seg
		}
		return &node{leaf: true, bounds: bounds, segments: segs}
	}
	axis := bounds.longestAxis()
	sort.Slice(items, func(i, j int) bool {
		return items[i].center[axis] < items[j].center[axis]
	})
	mid := len(items) / 2
	return &node{
		bounds: bounds,
		left:   buildRecursive(items[:mid], maxLeafSize),
		right:  buildRecursive(items[mid:], maxLeafSize),
	}
}

func centerOf(s Segment) [Dim]float64 {
	b := s.Bounds()
	var c [Dim]float64
	for d := 0; d < Dim; d++ {
		c[d] = 0.5 * (b.Min[d] + b.Max[d])
	}
	return c
}

func boundsOfItems(items []buildItem) AABB4 {
	b := items[0].seg.Bounds()
	for _, it := range items[1:] {
		b = b.Union(it.seg.Bounds())
	}
	return b
}

// split turns an over-full leaf into an internal node with two leaf
// children, partitioning segments at the median along the longest axis.
func (t *BVH) split(n *node) {
	axis := n.bounds.longestAxis()
	segs := n.segments
	sort.Slice(segs, func(i, j int) bool {
		return centerAlong(segs[i], axis) < centerAlong(segs[j], axis)
	})
	mid := len(segs) / 2
	n.leaf = false
	// Copy into fresh slices: the two children must not share a backing
	// array, otherwise a later append to one leaf would overwrite the
	// other leaf's segments and desync its bounds.
	leftSegs := append([]Segment(nil), segs[:mid]...)
	rightSegs := append([]Segment(nil), segs[mid:]...)
	n.left = &node{leaf: true, segments: leftSegs, bounds: boundsOf(leftSegs)}
	n.right = &node{leaf: true, segments: rightSegs, bounds: boundsOf(rightSegs)}
	n.segments = nil
}

func centerAlong(s Segment, axis int) float64 {
	b := s.Bounds()
	return 0.5 * (b.Min[axis] + b.Max[axis])
}

func boundsOf(segs []Segment) AABB4 {
	b := segs[0].Bounds()
	for _, s := range segs[1:] {
		b = b.Union(s.Bounds())
	}
	return b
}

// Query returns all indexed segments whose 4D AABB intersects the query
// box. This is the fast, conservative (superset) answer produced purely by
// BVH pruning; use QueryExact for geometrically exact results.
func (t *BVH) Query(q AABB4) []Segment {
	var out []Segment
	t.stats = Stats{}
	t.walk(t.root, q, func(s Segment) {
		if s.Bounds().Intersects(q) {
			out = append(out, s)
		}
	})
	return out
}

// Hit describes the exact portion of a segment lying inside the query box.
type Hit struct {
	Segment           Segment
	EnterT, ExitT     float64 // timestamps of entering / leaving the box
	EnterPos, ExitPos Vec3    // 3D positions at EnterT / ExitT
}

// QueryExact returns the segments that genuinely pass through the query box
// (a 4D line-segment vs 4D box intersection), with entry/exit times and
// positions of the clipped portion.
func (t *BVH) QueryExact(q AABB4) []Hit {
	var out []Hit
	t.stats = Stats{}
	t.walk(t.root, q, func(s Segment) {
		if u0, u1, ok := clipSegment(s, q); ok {
			out = append(out, Hit{
				Segment:  s,
				EnterT:   lerp(s.T0, s.T1, u0),
				ExitT:    lerp(s.T0, s.T1, u1),
				EnterPos: lerpVec(s.Start, s.End, u0),
				ExitPos:  lerpVec(s.Start, s.End, u1),
			})
		}
	})
	return out
}

// walk visits every leaf segment whose node bounds intersect q.
func (t *BVH) walk(n *node, q AABB4, visit func(Segment)) {
	if n == nil || !n.bounds.Intersects(q) {
		return
	}
	t.stats.NodesVisited++
	if n.leaf {
		t.stats.LeavesVisited++
		t.stats.SegmentsScanned += len(n.segments)
		for _, s := range n.segments {
			visit(s)
		}
		return
	}
	t.walk(n.left, q, visit)
	t.walk(n.right, q, visit)
}

// clipSegment intersects the 4D line segment s with the 4D box using the
// slab method. It returns the parameter range [u0, u1] (0..1 along s) that
// lies inside the box, or ok=false if the segment misses the box entirely.
func clipSegment(s Segment, box AABB4) (u0, u1 float64, ok bool) {
	a := [Dim]float64{s.Start.X, s.Start.Y, s.Start.Z, s.T0}
	b := [Dim]float64{s.End.X, s.End.Y, s.End.Z, s.T1}
	u0, u1 = 0.0, 1.0
	for d := 0; d < Dim; d++ {
		dir := b[d] - a[d]
		if math.Abs(dir) < 1e-12 {
			// Segment is constant along this axis: must already be inside.
			if a[d] < box.Min[d] || a[d] > box.Max[d] {
				return 0, 0, false
			}
			continue
		}
		t0 := (box.Min[d] - a[d]) / dir
		t1 := (box.Max[d] - a[d]) / dir
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		if t0 > u0 {
			u0 = t0
		}
		if t1 < u1 {
			u1 = t1
		}
		if u0 > u1 {
			return 0, 0, false
		}
	}
	return u0, u1, true
}

func lerp(a, b, u float64) float64 { return a + (b-a)*u }

func lerpVec(a, b Vec3, u float64) Vec3 {
	return Vec3{lerp(a.X, b.X, u), lerp(a.Y, b.Y, u), lerp(a.Z, b.Z, u)}
}
