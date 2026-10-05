// Package sparse2d implements an online 2D index over a sparse point set.
//
// Points carry a stable string ID, int64 coordinates and an int64 weight.
// Coordinates may appear in any order at any time: the index compresses
// them on the fly (sorted unique-coordinate arrays plus Fenwick trees), so
// no fixed compression table is ever assumed. Points sharing the same
// coordinates but holding different IDs are stored and counted separately.
//
// Supported operations:
//   - Add / Remove / Replace. Replace is atomic: the old and new versions
//     of an ID are never simultaneously visible or invisible to queries.
//   - Query: count and weight sum inside a half-open rectangle
//     [MinX,MaxX) x [MinY,MaxY).
//   - Scan: a full-scan reference implementation used to cross-check Query.
//
// Weight sums are accumulated in exact 128-bit arithmetic, so results are
// independent of insertion/iteration order. If the exact total does not
// fit in int64, Result.Sum saturates to math.MaxInt64 / math.MinInt64 and
// Result.Overflow is set. Exactness holds while the true total fits in
// 128 bits, which covers any physically representable input.
//
// Queries report per-call index-access statistics; the index also keeps
// cumulative totals (Stats / ResetStats).
package sparse2d

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"sync"
)

// Point is a single weighted sample identified by a stable ID.
type Point struct {
	ID string
	X  int64
	Y  int64
	W  int64
}

// Rect is a half-open axis-aligned rectangle: [MinX,MaxX) x [MinY,MaxY).
// Points lying exactly on MinX/MinY are included; points on MaxX/MaxY are
// excluded.
type Rect struct {
	MinX int64
	MinY int64
	MaxX int64
	MaxY int64
}

// Contains reports whether (x, y) falls inside the half-open rectangle.
func (r Rect) Contains(x, y int64) bool {
	return x >= r.MinX && x < r.MaxX && y >= r.MinY && y < r.MaxY
}

// QueryStats counts index accesses performed by a single operation.
type QueryStats struct {
	XNodesVisited int64 // distinct x-coordinate nodes touched
	FenwickReads  int64 // Fenwick tree cells read
	FenwickWrites int64 // Fenwick tree cells written (mutations only)
	SlotRebuilds  int64 // per-x coordinate-table rebuilds (first time a y is seen)
}

// Totals accumulates access statistics across all operations since the
// last ResetStats.
type Totals struct {
	Queries       int64
	Mutations     int64
	XNodesVisited int64
	FenwickReads  int64
	FenwickWrites int64
	SlotRebuilds  int64
}

// Result is the outcome of a rectangle query.
type Result struct {
	Count int64
	Sum   int64
	// Overflow is true when the exact weight sum does not fit in int64.
	// In that case Sum is saturated: math.MaxInt64 for positive overflow,
	// math.MinInt64 for negative overflow.
	Overflow bool
	Stats    QueryStats
}

var (
	ErrDuplicateID = errors.New("sparse2d: point ID already exists")
	ErrMissingID   = errors.New("sparse2d: point ID does not exist")
	ErrBadRect     = errors.New("sparse2d: rectangle min must be <= max on both axes")
)

// i128 is a signed 128-bit integer in two's complement. hi is the high
// 64 bits (sign-extended), lo the low 64 bits.
type i128 struct {
	hi uint64
	lo uint64
}

func i128FromInt64(v int64) i128 {
	return i128{hi: uint64(v >> 63), lo: uint64(v)}
}

func add128(a, b i128) i128 {
	lo, carry := bits.Add64(a.lo, b.lo, 0)
	return i128{hi: a.hi + b.hi + carry, lo: lo}
}

func neg128(a i128) i128 {
	lo, borrow := bits.Sub64(0, a.lo, 0)
	return i128{hi: 0 - a.hi - borrow, lo: lo}
}

// fitsInt64 reports whether the exact value fits in a signed int64.
func (a i128) fitsInt64() bool {
	return a.hi == uint64(int64(a.lo)>>63)
}

// sign returns -1, 0 or +1.
func (a i128) sign() int {
	switch {
	case a.hi == 0 && a.lo == 0:
		return 0
	case int64(a.hi) < 0:
		return -1
	default:
		return 1
	}
}

// saturate converts an out-of-int64-range value to the nearest bound.
func (a i128) saturate() int64 {
	if a.sign() < 0 {
		return math.MinInt64
	}
	return math.MaxInt64
}

// xNode holds every point sharing one x coordinate, indexed by y.
type xNode struct {
	ys      []int64       // sorted unique y values
	slotOf  map[int64]int // y -> slot index into ys
	baseCnt []int64       // per-slot live point count
	baseSum []i128        // per-slot exact sum of weights
	fenCnt  []int64       // Fenwick tree over baseCnt, 1-based
	fenSum  []i128        // Fenwick tree over baseSum, 1-based
}

func newXNode() *xNode {
	return &xNode{slotOf: make(map[int64]int)}
}

// slot returns the index of y, inserting it (and rebuilding the Fenwick
// trees) when this y is seen for the first time at this x.
func (n *xNode) slot(y int64, st *QueryStats) int {
	if i, ok := n.slotOf[y]; ok {
		return i
	}
	pos := sort.Search(len(n.ys), func(i int) bool { return n.ys[i] >= y })
	n.ys = append(n.ys, 0)
	copy(n.ys[pos+1:], n.ys[pos:])
	n.ys[pos] = y
	n.baseCnt = append(n.baseCnt, 0)
	copy(n.baseCnt[pos+1:], n.baseCnt[pos:])
	n.baseCnt[pos] = 0
	n.baseSum = append(n.baseSum, i128{})
	copy(n.baseSum[pos+1:], n.baseSum[pos:])
	n.baseSum[pos] = i128{}
	n.slotOf = make(map[int64]int, len(n.ys))
	for i, v := range n.ys {
		n.slotOf[v] = i
	}
	n.rebuild()
	if st != nil {
		st.SlotRebuilds++
	}
	return pos
}

func (n *xNode) rebuild() {
	m := len(n.ys)
	n.fenCnt = make([]int64, m+1)
	n.fenSum = make([]i128, m+1)
	for i := 0; i < m; i++ {
		fenwickAdd(n.fenCnt, i, n.baseCnt[i], nil)
		fenwickAdd128(n.fenSum, i, n.baseSum[i], nil)
	}
}

func fenwickAdd(tree []int64, i int, delta int64, st *QueryStats) {
	for i++; i < len(tree); i += i & (-i) {
		tree[i] += delta
		if st != nil {
			st.FenwickWrites++
		}
	}
}

func fenwickAdd128(tree []i128, i int, delta i128, st *QueryStats) {
	for i++; i < len(tree); i += i & (-i) {
		tree[i] = add128(tree[i], delta)
		if st != nil {
			st.FenwickWrites++
		}
	}
}

// fenwickPrefix returns the sum of slots [0, i).
func fenwickPrefix(tree []int64, i int, st *QueryStats) int64 {
	var acc int64
	for ; i > 0; i -= i & (-i) {
		acc += tree[i]
		if st != nil {
			st.FenwickReads++
		}
	}
	return acc
}

func fenwickPrefix128(tree []i128, i int, st *QueryStats) i128 {
	var acc i128
	for ; i > 0; i -= i & (-i) {
		acc = add128(acc, tree[i])
		if st != nil {
			st.FenwickReads++
		}
	}
	return acc
}

// lowerIdx returns the number of stored y values strictly less than v.
func (n *xNode) lowerIdx(v int64) int {
	return sort.Search(len(n.ys), func(i int) bool { return n.ys[i] >= v })
}

// bounds translates the half-open y range [lo, hi) to slot indices.
func (n *xNode) bounds(lo, hi int64) (int, int) {
	return n.lowerIdx(lo), n.lowerIdx(hi)
}

func (n *xNode) rangeCount(lo, hi int64, st *QueryStats) int64 {
	a, b := n.bounds(lo, hi)
	if a >= b {
		return 0
	}
	return fenwickPrefix(n.fenCnt, b, st) - fenwickPrefix(n.fenCnt, a, st)
}

func (n *xNode) rangeSum(lo, hi int64, st *QueryStats) i128 {
	a, b := n.bounds(lo, hi)
	if a >= b {
		return i128{}
	}
	// Prefix difference is exact in 128-bit two's complement.
	return add128(fenwickPrefix128(n.fenSum, b, st), neg128(fenwickPrefix128(n.fenSum, a, st)))
}

// Index is a goroutine-safe online 2D point index.
type Index struct {
	mu     sync.RWMutex
	points map[string]Point // ID -> live point
	xs     []int64          // sorted unique x values
	nodes  map[int64]*xNode // x -> per-x y index
	totals Totals
}

// New returns an empty index.
func New() *Index {
	return &Index{points: make(map[string]Point), nodes: make(map[int64]*xNode)}
}

// Len returns the number of live points.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.points)
}

// Get returns the live point for id.
func (ix *Index) Get(id string) (Point, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	p, ok := ix.points[id]
	return p, ok
}

// Add inserts p. It fails if p.ID is already present.
func (ix *Index) Add(p Point) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.points[p.ID]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateID, p.ID)
	}
	var st QueryStats
	ix.insertLocked(p, &st)
	ix.accumulateLocked(&st, true)
	return nil
}

// Remove deletes the point with the given ID.
func (ix *Index) Remove(id string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	p, ok := ix.points[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMissingID, id)
	}
	var st QueryStats
	ix.removeLocked(p, &st)
	ix.accumulateLocked(&st, true)
	return nil
}

// Replace atomically swaps the live point for p.ID with p. Queries never
// observe an intermediate state: the old and new versions are exchanged
// under a single write lock. It fails if p.ID is not present.
func (ix *Index) Replace(p Point) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	old, ok := ix.points[p.ID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrMissingID, p.ID)
	}
	var st QueryStats
	ix.removeLocked(old, &st)
	ix.insertLocked(p, &st)
	ix.accumulateLocked(&st, true)
	return nil
}

func (ix *Index) insertLocked(p Point, st *QueryStats) {
	node, ok := ix.nodes[p.X]
	if !ok {
		node = newXNode()
		ix.nodes[p.X] = node
		pos := sort.Search(len(ix.xs), func(i int) bool { return ix.xs[i] >= p.X })
		ix.xs = append(ix.xs, 0)
		copy(ix.xs[pos+1:], ix.xs[pos:])
		ix.xs[pos] = p.X
	}
	s := node.slot(p.Y, st)
	w := i128FromInt64(p.W)
	node.baseCnt[s]++
	node.baseSum[s] = add128(node.baseSum[s], w)
	fenwickAdd(node.fenCnt, s, 1, st)
	fenwickAdd128(node.fenSum, s, w, st)
	ix.points[p.ID] = p
}

func (ix *Index) removeLocked(p Point, st *QueryStats) {
	node := ix.nodes[p.X]
	s := node.slotOf[p.Y] // guaranteed to exist: y slots are never deleted
	w := i128FromInt64(p.W)
	node.baseCnt[s]--
	node.baseSum[s] = add128(node.baseSum[s], neg128(w))
	fenwickAdd(node.fenCnt, s, -1, st)
	fenwickAdd128(node.fenSum, s, neg128(w), st)
	delete(ix.points, p.ID)
	// The y slot and x node are kept (lazily) so future inserts stay O(log).
}

func (ix *Index) accumulateLocked(st *QueryStats, mutation bool) {
	if mutation {
		ix.totals.Mutations++
	} else {
		ix.totals.Queries++
	}
	ix.totals.XNodesVisited += st.XNodesVisited
	ix.totals.FenwickReads += st.FenwickReads
	ix.totals.FenwickWrites += st.FenwickWrites
	ix.totals.SlotRebuilds += st.SlotRebuilds
}

// Query returns the count and weight sum of points inside r using the
// index. Ranges are half-open on both axes.
func (ix *Index) Query(r Rect) (Result, error) {
	if r.MinX > r.MaxX || r.MinY > r.MaxY {
		return Result{}, ErrBadRect
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	res := ix.queryLocked(r)
	ix.accumulateLocked(&res.Stats, false)
	return res, nil
}

func (ix *Index) queryLocked(r Rect) Result {
	var res Result
	var sum i128
	lo := sort.Search(len(ix.xs), func(i int) bool { return ix.xs[i] >= r.MinX })
	hi := sort.Search(len(ix.xs), func(i int) bool { return ix.xs[i] >= r.MaxX })
	for xi := lo; xi < hi; xi++ {
		node := ix.nodes[ix.xs[xi]]
		res.Stats.XNodesVisited++
		res.Count += node.rangeCount(r.MinY, r.MaxY, &res.Stats)
		sum = add128(sum, node.rangeSum(r.MinY, r.MaxY, &res.Stats))
	}
	if sum.fitsInt64() {
		res.Sum = int64(sum.lo)
	} else {
		res.Sum = sum.saturate()
		res.Overflow = true
	}
	return res
}

// Scan is the full-scan reference implementation: it walks every live
// point and applies the same half-open bounds and overflow rules. It is
// O(n) and intended for cross-checking Query.
func (ix *Index) Scan(r Rect) (Result, error) {
	if r.MinX > r.MaxX || r.MinY > r.MaxY {
		return Result{}, ErrBadRect
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var res Result
	var sum i128
	for _, p := range ix.points {
		res.Stats.XNodesVisited++ // one "access" per point examined
		if !r.Contains(p.X, p.Y) {
			continue
		}
		res.Count++
		sum = add128(sum, i128FromInt64(p.W))
	}
	if sum.fitsInt64() {
		res.Sum = int64(sum.lo)
	} else {
		res.Sum = sum.saturate()
		res.Overflow = true
	}
	return res, nil
}

// Stats returns a snapshot of the cumulative access statistics.
func (ix *Index) Stats() Totals {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.totals
}

// ResetStats zeroes the cumulative access statistics.
func (ix *Index) ResetStats() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.totals = Totals{}
}
