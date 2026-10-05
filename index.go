// Package sparse2d implements an online 2D orthogonal range index over a
// sparse point set. Points are keyed by a stable string ID and carry an
// int64 weight. Queries report the count and the weight sum of all points
// inside a half-open rectangle [x0,x1) x [y0,y1).
//
// Coordinates may appear online in any order; no precomputed compression
// table is required. The index is a two-level treap: an outer treap keyed
// by x (one node per distinct x), each holding an inner treap keyed by
// (y, id) with subtree aggregates (count, wrapped weight sum, carry).
// Treap priorities are deterministic hashes of the keys, so behaviour is
// reproducible across runs.
//
// Duplicate coordinates under different IDs are stored and charged
// separately. Replace is atomic: guarded by the index lock, no concurrent
// reader can observe the intermediate state between remove and add.
//
// Integer overflow: weights are int64. Every subtree tracks its exact sum
// as (sum + carry * 2^64), where sum is the wrapped int64 value and carry
// counts the multiples of 2^64. Because two's-complement addition is
// associative modulo 2^64, Result.Sum always equals the exact
// mathematical sum reduced mod 2^64, and Result.Overflow reports exactly
// whether that mathematical sum lies outside the int64 range. The carry
// itself cannot overflow: its magnitude is bounded by the point count.
package sparse2d

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
)

// Point is a single weighted point.
type Point struct {
	X, Y   int64
	Weight int64
}

// Result is the outcome of a rectangle query.
type Result struct {
	Count int64 // number of points inside the rectangle
	// Sum is the exact mathematical weight sum reduced modulo 2^64 into
	// the int64 range. It is exact whenever Overflow is false.
	Sum int64
	// Overflow reports whether the exact mathematical weight sum lies
	// outside the int64 range.
	Overflow bool
}

// accumulator accumulates exact sums as (sum + carry * 2^64).
type accumulator struct {
	count int64
	sum   int64
	carry int64
}

func (a *accumulator) add(count, sum, carry int64) {
	a.count += count
	a.sum += sum
	a.carry += carry + carryOf(a.sum, sum)
}

// carryOf returns the number of 2^64 multiples lost when computing a+b
// with wrapping int64 arithmetic: 0, +1 or -1. It must be called with
// the already-wrapped sum s = a + b.
func carryOf(s, b int64) int64 {
	// exact = a + b = s + k*2^64 with a = s - b (exact, since k in {-1,0,1})
	switch {
	case b > 0 && s < s-b:
		return 1
	case b < 0 && s > s-b:
		return -1
	}
	return 0
}

// Stats reports cumulative index access statistics.
type Stats struct {
	Queries    int64 // rectangle queries served
	Inserts    int64 // points inserted (including the add half of Replace)
	Removes    int64 // points removed (including the remove half of Replace)
	Replaces   int64 // atomic replace operations
	NodeVisits int64 // treap nodes examined while serving queries
}

// Index is a 2D range index over a sparse point set. It is safe for
// concurrent use; Replace is atomic with respect to other operations.
type Index struct {
	mu     sync.RWMutex
	root   *outerNode
	points map[string]Point
	xCount int64 // number of distinct x coordinates

	queries    atomic.Int64
	inserts    atomic.Int64
	removes    atomic.Int64
	replaces   atomic.Int64
	nodeVisits atomic.Int64
}

// New returns an empty index.
func New() *Index {
	return &Index{points: make(map[string]Point)}
}

// Len returns the number of points in the index.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.points)
}

// DistinctX returns the number of distinct x coordinates present.
func (ix *Index) DistinctX() int64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.xCount
}

// Get returns the point stored under id.
func (ix *Index) Get(id string) (Point, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	p, ok := ix.points[id]
	return p, ok
}

// Add inserts a point under id. It returns false and leaves the index
// unchanged if id already exists.
func (ix *Index) Add(id string, x, y, weight int64) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.points[id]; ok {
		return false
	}
	ix.addPoint(id, x, y, weight)
	ix.inserts.Add(1)
	return true
}

// Remove deletes the point stored under id. It returns false if id is
// not present.
func (ix *Index) Remove(id string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	p, ok := ix.points[id]
	if !ok {
		return false
	}
	ix.removePoint(id, p)
	ix.removes.Add(1)
	return true
}

// Replace atomically moves id to a new coordinate and weight. It returns
// false (and changes nothing) if id is not present. Holding the index
// write lock for the whole remove+add pair makes the operation atomic:
// no concurrent query can observe the intermediate state.
func (ix *Index) Replace(id string, x, y, weight int64) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	old, ok := ix.points[id]
	if !ok {
		return false
	}
	ix.removePoint(id, old)
	ix.addPoint(id, x, y, weight)
	ix.replaces.Add(1)
	return true
}

// Query returns the count and weight sum of all points inside the
// half-open rectangle [x0,x1) x [y0,y1). An empty or inverted rectangle
// (x1 <= x0 or y1 <= y0) yields a zero Result.
//
// Note: the upper bounds are exclusive int64 values, so a point at
// math.MaxInt64 on an axis cannot be covered (its successor is not
// representable). All other coordinates, including math.MinInt64, work.
func (ix *Index) Query(x0, y0, x1, y1 int64) Result {
	ix.queries.Add(1)
	if x1 <= x0 || y1 <= y0 {
		return Result{}
	}
	ix.mu.RLock()
	var acc accumulator
	ix.queryOuter(ix.root, x0, x1, y0, y1, &acc)
	ix.mu.RUnlock()
	return Result{Count: acc.count, Sum: acc.sum, Overflow: acc.carry != 0}
}

// Stats returns a snapshot of the cumulative access statistics.
func (ix *Index) Stats() Stats {
	return Stats{
		Queries:    ix.queries.Load(),
		Inserts:    ix.inserts.Load(),
		Removes:    ix.removes.Load(),
		Replaces:   ix.replaces.Load(),
		NodeVisits: ix.nodeVisits.Load(),
	}
}

// ResetStats zeroes all access statistics.
func (ix *Index) ResetStats() {
	ix.queries.Store(0)
	ix.inserts.Store(0)
	ix.removes.Store(0)
	ix.replaces.Store(0)
	ix.nodeVisits.Store(0)
}

func (ix *Index) addPoint(id string, x, y, weight int64) {
	ix.points[id] = Point{X: x, Y: y, Weight: weight}
	on := ix.outerGet(x)
	if on == nil {
		on = &outerNode{x: x, prio: hashX(x)}
		ix.root = outerInsert(ix.root, on)
		ix.xCount++
	}
	in := &innerNode{y: y, id: id, w: weight, prio: hashYID(y, id)}
	in.pull()
	on.inner = innerInsert(on.inner, in)
}

func (ix *Index) removePoint(id string, p Point) {
	delete(ix.points, id)
	on := ix.outerGet(p.X)
	on.inner, _ = innerDelete(on.inner, p.Y, id)
	if on.inner == nil {
		ix.root = outerDelete(ix.root, p.X)
		ix.xCount--
	}
}

func (ix *Index) outerGet(x int64) *outerNode {
	for n := ix.root; n != nil; {
		switch {
		case x < n.x:
			n = n.left
		case x > n.x:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

func (ix *Index) queryOuter(n *outerNode, x0, x1, y0, y1 int64, acc *accumulator) {
	for n != nil {
		ix.nodeVisits.Add(1)
		switch {
		case n.x >= x1:
			n = n.left
		case n.x < x0:
			n = n.right
		default:
			innerQuery(n.inner, y0, y1, acc, &ix.nodeVisits)
			ix.queryOuter(n.left, x0, x1, y0, y1, acc)
			n = n.right
		}
	}
}

// ---------------------------------------------------------------------------
// inner treap: keyed by (y, id), augmented with subtree aggregates
// ---------------------------------------------------------------------------

type innerNode struct {
	y    int64
	id   string
	w    int64
	prio uint64

	left, right *innerNode

	count int64 // points in subtree
	sum   int64 // subtree weight sum wrapped mod 2^64
	carry int64 // multiples of 2^64 in the exact subtree sum
	minY  int64 // min y in subtree (for whole-subtree pruning)
	maxY  int64 // max y in subtree
}

func lessYID(y1 int64, id1 string, y2 int64, id2 string) bool {
	if y1 != y2 {
		return y1 < y2
	}
	return id1 < id2
}

func (n *innerNode) pull() {
	n.count = 1
	n.sum = n.w
	n.carry = 0
	n.minY, n.maxY = n.y, n.y
	for _, c := range [2]*innerNode{n.left, n.right} {
		if c == nil {
			continue
		}
		n.count += c.count
		n.sum += c.sum
		n.carry += c.carry + carryOf(n.sum, c.sum)
		if c.minY < n.minY {
			n.minY = c.minY
		}
		if c.maxY > n.maxY {
			n.maxY = c.maxY
		}
	}
}

// innerQuery accumulates aggregates of all points with y in [y0, y1).
func innerQuery(n *innerNode, y0, y1 int64, acc *accumulator, visits *atomic.Int64) {
	if n == nil {
		return
	}
	visits.Add(1)
	if n.maxY < y0 || n.minY >= y1 {
		return // whole subtree outside the y range
	}
	if n.minY >= y0 && n.maxY < y1 {
		acc.add(n.count, n.sum, n.carry) // whole subtree inside
		return
	}
	if n.y >= y0 && n.y < y1 {
		acc.add(1, n.w, 0)
	}
	innerQuery(n.left, y0, y1, acc, visits)
	innerQuery(n.right, y0, y1, acc, visits)
}

func innerInsert(n, nn *innerNode) *innerNode {
	if n == nil {
		return nn
	}
	if nn.prio < n.prio {
		l, r := innerSplit(n, nn.y, nn.id)
		nn.left, nn.right = l, r
		nn.pull()
		return nn
	}
	if lessYID(nn.y, nn.id, n.y, n.id) {
		n.left = innerInsert(n.left, nn)
	} else {
		n.right = innerInsert(n.right, nn)
	}
	n.pull()
	return n
}

// innerSplit splits n into keys < (y,id) and keys >= (y,id).
func innerSplit(n *innerNode, y int64, id string) (l, r *innerNode) {
	if n == nil {
		return nil, nil
	}
	if lessYID(n.y, n.id, y, id) {
		rl, rr := innerSplit(n.right, y, id)
		n.right = rl
		n.pull()
		return n, rr
	}
	ll, lr := innerSplit(n.left, y, id)
	n.left = lr
	n.pull()
	return ll, n
}

// innerMerge merges two treaps where all keys of a precede all keys of b.
func innerMerge(a, b *innerNode) *innerNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.right = innerMerge(a.right, b)
		a.pull()
		return a
	}
	b.left = innerMerge(a, b.left)
	b.pull()
	return b
}

// innerDelete removes key (y,id), returning the new root and the removed
// node (nil if absent).
func innerDelete(n *innerNode, y int64, id string) (*innerNode, *innerNode) {
	if n == nil {
		return nil, nil
	}
	switch {
	case lessYID(y, id, n.y, n.id):
		var rem *innerNode
		n.left, rem = innerDelete(n.left, y, id)
		n.pull()
		return n, rem
	case lessYID(n.y, n.id, y, id):
		var rem *innerNode
		n.right, rem = innerDelete(n.right, y, id)
		n.pull()
		return n, rem
	default:
		return innerMerge(n.left, n.right), n
	}
}

// ---------------------------------------------------------------------------
// outer treap: keyed by x, one node per distinct x coordinate
// ---------------------------------------------------------------------------

type outerNode struct {
	x    int64
	prio uint64

	left, right *outerNode
	inner       *innerNode
}

func outerInsert(n, nn *outerNode) *outerNode {
	if n == nil {
		return nn
	}
	if nn.prio < n.prio {
		l, r := outerSplit(n, nn.x)
		nn.left, nn.right = l, r
		return nn
	}
	if nn.x < n.x {
		n.left = outerInsert(n.left, nn)
	} else {
		n.right = outerInsert(n.right, nn)
	}
	return n
}

// outerSplit splits n into keys < x and keys >= x.
func outerSplit(n *outerNode, x int64) (l, r *outerNode) {
	if n == nil {
		return nil, nil
	}
	if n.x < x {
		rl, rr := outerSplit(n.right, x)
		n.right = rl
		return n, rr
	}
	ll, lr := outerSplit(n.left, x)
	n.left = lr
	return ll, n
}

func outerMerge(a, b *outerNode) *outerNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.right = outerMerge(a.right, b)
		return a
	}
	b.left = outerMerge(a, b.left)
	return b
}

func outerDelete(n *outerNode, x int64) *outerNode {
	if n == nil {
		return nil
	}
	switch {
	case x < n.x:
		n.left = outerDelete(n.left, x)
	case x > n.x:
		n.right = outerDelete(n.right, x)
	default:
		return outerMerge(n.left, n.right)
	}
	return n
}

// ---------------------------------------------------------------------------
// deterministic priorities (splitmix64 mixes of the keys)
// ---------------------------------------------------------------------------

func mix64(z uint64) uint64 {
	z ^= z >> 30
	z *= 0xbf58476d1ce4e5b9
	z ^= z >> 27
	z *= 0x94d049bb133111eb
	z ^= z >> 31
	return z
}

func hashX(x int64) uint64 {
	return mix64(uint64(x) ^ 0x9e3779b97f4a7c15)
}

func hashYID(y int64, id string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	return mix64(mix64(uint64(y)) ^ h.Sum64())
}
