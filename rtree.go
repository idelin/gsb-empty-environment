// Package rtree implements a two-dimensional R-Tree spatial index with
// R*-style forced reinsertion and Guttman's quadratic split.
//
// The tree stores axis-aligned bounding rectangles (e.g. the minimum
// bounding rectangle of a polygon, or a zero-area rect for a point) and
// answers rectangular window (bounding box) queries while pruning
// branches whose bounding box does not intersect the query window.
package rtree

import (
	"math"
	"sort"
)

// Rect is an axis-aligned bounding rectangle. Min must be <= Max on both
// axes. A point is represented as a Rect with Min == Max.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

// PointRect builds a zero-area Rect for a point (e.g. a shop's lng/lat).
func PointRect(x, y float64) Rect { return Rect{MinX: x, MinY: y, MaxX: x, MaxY: y} }

func (r Rect) area() float64 { return (r.MaxX - r.MinX) * (r.MaxY - r.MinY) }

func (r Rect) union(o Rect) Rect {
	return Rect{
		MinX: math.Min(r.MinX, o.MinX),
		MinY: math.Min(r.MinY, o.MinY),
		MaxX: math.Max(r.MaxX, o.MaxX),
		MaxY: math.Max(r.MaxY, o.MaxY),
	}
}

// Intersects reports whether r and o overlap (touching edges count).
func (r Rect) Intersects(o Rect) bool {
	return r.MinX <= o.MaxX && o.MinX <= r.MaxX &&
		r.MinY <= o.MaxY && o.MinY <= r.MaxY
}

// Contains reports whether o is fully inside r.
func (r Rect) Contains(o Rect) bool {
	return r.MinX <= o.MinX && r.MinY <= o.MinY &&
		r.MaxX >= o.MaxX && r.MaxY >= o.MaxY
}

func (r Rect) center() (float64, float64) {
	return (r.MinX + r.MaxX) / 2, (r.MinY + r.MaxY) / 2
}

// enlargement returns the area increase needed for r to cover o.
func (r Rect) enlargement(o Rect) float64 {
	return r.union(o).area() - r.area()
}

// entry is one slot in a node. Leaf entries carry user Data; internal
// entries point to a Child node.
type entry struct {
	rect  Rect
	child *node       // non-nil in internal nodes
	data  interface{} // non-nil in leaf nodes
}

type node struct {
	leaf    bool
	entries []*entry
}

func (n *node) boundingBox() Rect {
	b := n.entries[0].rect
	for _, e := range n.entries[1:] {
		b = b.union(e.rect)
	}
	return b
}

// RTree is a 2D R-Tree. The zero value is not usable; call New.
type RTree struct {
	root       *node
	height     int // height of the root; leaves are at height 0
	size       int
	maxEntries int
	minEntries int
	reinsertP  int // number of entries reinserted on first overflow
}

// New creates an empty R-Tree whose nodes hold at most maxEntries
// entries (typically 8..50; clamped to >= 4).
func New(maxEntries int) *RTree {
	if maxEntries < 4 {
		maxEntries = 4
	}
	p := int(math.Round(0.3 * float64(maxEntries))) // R* paper: p = 30%
	if p < 1 {
		p = 1
	}
	return &RTree{
		root:       &node{leaf: true},
		height:     0,
		maxEntries: maxEntries,
		minEntries: int(math.Ceil(0.4 * float64(maxEntries))),
		reinsertP:  p,
	}
}

// Size returns the number of stored entries.
func (t *RTree) Size() int { return t.size }

// Height returns the height of the root (a tree of only leaves has 0).
func (t *RTree) Height() int { return t.height }

// BoundingBox returns the minimum rectangle covering all entries.
// The second return value is false for an empty tree.
func (t *RTree) BoundingBox() (Rect, bool) {
	if t.size == 0 {
		return Rect{}, false
	}
	return t.root.boundingBox(), true
}

// Insert stores data under the bounding rectangle r. For a polygon, pass
// its minimum bounding rectangle; for a point, use PointRect.
func (t *RTree) Insert(r Rect, data interface{}) {
	reinserted := make(map[int]bool) // levels already reinserted in this call
	if split := t.insertRec(t.root, &entry{rect: r, data: data}, 0, t.height, reinserted); split != nil {
		t.growRoot(split)
	}
	t.size++
}

// insertRec inserts e into the subtree rooted at n (which sits at the
// given height) so that e lands in a node at level `level`. It returns a
// new sibling node if n was split, or nil otherwise.
func (t *RTree) insertRec(n *node, e *entry, level, height int, reinserted map[int]bool) *node {
	if height == level {
		n.entries = append(n.entries, e)
	} else {
		best := chooseSubtree(n, e.rect)
		split := t.insertRec(best.child, e, level, height-1, reinserted)
		best.rect = best.rect.union(e.rect)
		if split != nil {
			n.entries = append(n.entries, &entry{rect: split.boundingBox(), child: split})
		}
	}

	if len(n.entries) <= t.maxEntries {
		return nil
	}

	// Overflow: on the first overflow at this level during one top-level
	// insert, forcibly reinsert the p entries farthest from the node's
	// center (R*-tree). Otherwise fall back to quadratic split.
	if !reinserted[height] {
		reinserted[height] = true
		for _, re := range t.removeDistant(n) {
			t.reinsert(re, height, reinserted)
		}
		return nil
	}
	return t.quadraticSplit(n)
}

// reinsert inserts an entry that must land at the given level, growing
// the root if a split propagates all the way up.
func (t *RTree) reinsert(e *entry, level int, reinserted map[int]bool) {
	if split := t.insertRec(t.root, e, level, t.height, reinserted); split != nil {
		t.growRoot(split)
	}
}

func (t *RTree) growRoot(split *node) {
	t.root = &node{
		entries: []*entry{
			{rect: t.root.boundingBox(), child: t.root},
			{rect: split.boundingBox(), child: split},
		},
	}
	t.height++
}

// chooseSubtree picks the entry whose rectangle needs the least area
// enlargement; ties are broken by smaller area.
func chooseSubtree(n *node, r Rect) *entry {
	best := n.entries[0]
	bestEnl, bestArea := best.rect.enlargement(r), best.rect.area()
	for _, e := range n.entries[1:] {
		enl, ar := e.rect.enlargement(r), e.rect.area()
		if enl < bestEnl || (enl == bestEnl && ar < bestArea) {
			best, bestEnl, bestArea = e, enl, ar
		}
	}
	return best
}

// removeDistant removes and returns the p entries whose centers are
// farthest from the node's bounding-box center.
func (t *RTree) removeDistant(n *node) []*entry {
	bb := n.boundingBox()
	cx, cy := bb.center()
	type distEntry struct {
		e *entry
		d float64
	}
	scored := make([]distEntry, len(n.entries))
	for i, e := range n.entries {
		ex, ey := e.rect.center()
		dx, dy := ex-cx, ey-cy
		scored[i] = distEntry{e, dx*dx + dy*dy}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].d > scored[j].d })

	p := t.reinsertP
	removed := make([]*entry, p)
	for i := 0; i < p; i++ {
		removed[i] = scored[i].e
	}
	kept := make([]*entry, 0, len(n.entries)-p)
	for _, s := range scored[p:] {
		kept = append(kept, s.e)
	}
	n.entries = kept
	return removed
}

// quadraticSplit divides an overflowing node into two groups using
// Guttman's quadratic algorithm and returns the new sibling node.
func (t *RTree) quadraticSplit(n *node) *node {
	entries := n.entries

	// PickSeeds: the pair wasting the most area if put together.
	s1, s2 := 0, 1
	maxWaste := math.Inf(-1)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			waste := entries[i].rect.union(entries[j].rect).area() -
				entries[i].rect.area() - entries[j].rect.area()
			if waste > maxWaste {
				maxWaste, s1, s2 = waste, i, j
			}
		}
	}

	groupA := []*entry{entries[s1]}
	groupB := []*entry{entries[s2]}
	rectA := entries[s1].rect
	rectB := entries[s2].rect

	rest := make([]*entry, 0, len(entries)-2)
	for i, e := range entries {
		if i != s1 && i != s2 {
			rest = append(rest, e)
		}
	}

	// PickNext: assign the entry with the strongest group preference.
	for len(rest) > 0 {
		// Guarantee the minimum fill of both groups.
		if len(groupA)+len(rest) == t.minEntries {
			for _, e := range rest {
				groupA = append(groupA, e)
				rectA = rectA.union(e.rect)
			}
			break
		}
		if len(groupB)+len(rest) == t.minEntries {
			for _, e := range rest {
				groupB = append(groupB, e)
				rectB = rectB.union(e.rect)
			}
			break
		}

		bestIdx, bestDiff := 0, math.Inf(-1)
		for i, e := range rest {
			dA := rectA.enlargement(e.rect)
			dB := rectB.enlargement(e.rect)
			if diff := math.Abs(dA - dB); diff > bestDiff {
				bestIdx, bestDiff = i, diff
			}
		}
		e := rest[bestIdx]
		rest = append(rest[:bestIdx], rest[bestIdx+1:]...)

		dA := rectA.enlargement(e.rect)
		dB := rectB.enlargement(e.rect)
		switch {
		case dA < dB:
			groupA = append(groupA, e)
			rectA = rectA.union(e.rect)
		case dB < dA:
			groupB = append(groupB, e)
			rectB = rectB.union(e.rect)
		case rectA.area() < rectB.area():
			groupA = append(groupA, e)
			rectA = rectA.union(e.rect)
		case rectB.area() < rectA.area():
			groupB = append(groupB, e)
			rectB = rectB.union(e.rect)
		case len(groupA) <= len(groupB):
			groupA = append(groupA, e)
			rectA = rectA.union(e.rect)
		default:
			groupB = append(groupB, e)
			rectB = rectB.union(e.rect)
		}
	}

	n.entries = groupA
	return &node{leaf: n.leaf, entries: groupB}
}

// Search returns the data of every entry whose rectangle intersects the
// query window q.
func (t *RTree) Search(q Rect) []interface{} {
	out, _ := t.SearchWithStats(q)
	return out
}

// SearchWithStats behaves like Search and additionally reports how many
// tree nodes were visited, which demonstrates branch pruning.
func (t *RTree) SearchWithStats(q Rect) ([]interface{}, int) {
	var out []interface{}
	visited := 0
	t.searchRec(t.root, q, &out, &visited)
	return out, visited
}

func (t *RTree) searchRec(n *node, q Rect, out *[]interface{}, visited *int) {
	*visited++
	for _, e := range n.entries {
		if !e.rect.Intersects(q) {
			continue // prune: this branch cannot contain matches
		}
		if n.leaf {
			*out = append(*out, e.data)
		} else {
			t.searchRec(e.child, q, out, visited)
		}
	}
}

// TotalNodes returns the number of nodes in the tree (root included).
func (t *RTree) TotalNodes() int {
	count := 0
	var walk func(n *node)
	walk = func(n *node) {
		count++
		for _, e := range n.entries {
			if e.child != nil {
				walk(e.child)
			}
		}
	}
	walk(t.root)
	return count
}
