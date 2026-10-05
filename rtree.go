// Package rtree implements an R-tree style spatial index over 2D integer
// rectangles. Rectangles may degenerate into lines or points. Intersection
// uses closed intervals: touching boundaries count as intersecting.
//
// Structural rules:
//   - Every node holds between minFill = ceil(capacity/2) and capacity
//     entries, except the root (2..capacity, or 0..capacity when it is a
//     leaf).
//   - Split: quadratic split (Guttman). The overflowed node of capacity+1
//     entries is divided into two groups seeded by the pair maximizing dead
//     space; remaining entries are assigned by maximum preference
//     difference, each to the group needing the least enlargement (ties:
//     smaller area, then fewer entries). A group short of minFill absorbs
//     all remaining entries.
//   - Underflow: on delete, nodes dropping below minFill are detached and
//     their entries re-inserted at their original level (leaf objects at
//     level 0, internal children at their own level).
//   - Root: if the root overflows it is split and a new root one level
//     higher is created. If the root has a single child after deletions,
//     the child becomes the new root (repeated as needed). An emptied
//     internal root is replaced by an empty leaf.
//   - Every internal entry's rectangle is always the tight bounding box of
//     its child subtree.
package rtree

import (
	"errors"
	"fmt"
	"sort"
)

// ErrNotFound is returned when an operation references an unknown ID.
var ErrNotFound = errors.New("rtree: id not found")

// ErrDuplicate is returned when inserting an ID that already exists.
var ErrDuplicate = errors.New("rtree: duplicate id")

// Rect is an axis-aligned rectangle with inclusive bounds. It may be
// degenerate (a horizontal/vertical line or a single point).
type Rect struct {
	MinX, MinY, MaxX, MaxY int64
}

// NewRect builds a normalized Rect from two corners in any order.
func NewRect(x1, y1, x2, y2 int64) Rect {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	return Rect{MinX: x1, MinY: y1, MaxX: x2, MaxY: y2}
}

// Intersects reports closed-interval overlap; touching edges or corners
// count as intersecting.
func (r Rect) Intersects(o Rect) bool {
	return r.MinX <= o.MaxX && o.MinX <= r.MaxX &&
		r.MinY <= o.MaxY && o.MinY <= r.MaxY
}

func (r Rect) normalized() bool { return r.MinX <= r.MaxX && r.MinY <= r.MaxY }

// area returns the area as float64 to avoid int64 overflow on huge ranges.
func (r Rect) area() float64 {
	return (float64(r.MaxX) - float64(r.MinX) + 1) *
		(float64(r.MaxY) - float64(r.MinY) + 1)
}

func unionRect(a, b Rect) Rect {
	return Rect{
		MinX: min64(a.MinX, b.MinX),
		MinY: min64(a.MinY, b.MinY),
		MaxX: max64(a.MaxX, b.MaxX),
		MaxY: max64(a.MaxY, b.MaxY),
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type entry struct {
	rect  Rect  // leaf: object rect; internal: tight MBR of child
	id    int64 // leaf only
	child *node // internal only
}

type node struct {
	level   int // 0 = leaf
	entries []entry
	parent  *node
}

func (n *node) leaf() bool { return n.level == 0 }

func mbrOf(entries []entry) Rect {
	r := entries[0].rect
	for _, e := range entries[1:] {
		r = unionRect(r, e.rect)
	}
	return r
}

// Tree is an R-tree index. It is not safe for concurrent use.
type Tree struct {
	root     *node
	capacity int
	minFill  int
	size     int
	index    map[int64]*node // id -> leaf node holding it
}

// New creates an empty tree whose nodes hold at most capacity entries.
// capacity must be >= 4.
func New(capacity int) (*Tree, error) {
	if capacity < 4 {
		return nil, fmt.Errorf("rtree: capacity must be >= 4, got %d", capacity)
	}
	return &Tree{
		root:     &node{level: 0},
		capacity: capacity,
		minFill:  (capacity + 1) / 2,
		index:    make(map[int64]*node),
	}, nil
}

// Size returns the number of indexed objects.
func (t *Tree) Size() int { return t.size }

// Height returns the tree height in nodes (a tree with only a leaf root is 1).
func (t *Tree) Height() int { return t.root.level + 1 }

// Insert adds id with rectangle r. It fails with ErrDuplicate if id exists.
func (t *Tree) Insert(id int64, r Rect) error {
	if !r.normalized() {
		return fmt.Errorf("rtree: non-normalized rect %+v", r)
	}
	if _, ok := t.index[id]; ok {
		return ErrDuplicate
	}
	t.insertEntry(entry{rect: r, id: id}, 0)
	t.size++
	return nil
}

// Delete removes id. It fails with ErrNotFound if id is unknown. No ghost
// entries remain: the object is removed from its leaf and underflowing
// ancestors are re-inserted.
func (t *Tree) Delete(id int64) error {
	leaf, ok := t.index[id]
	if !ok {
		return ErrNotFound
	}
	for i := range leaf.entries {
		if leaf.entries[i].id == id {
			leaf.entries = append(leaf.entries[:i], leaf.entries[i+1:]...)
			break
		}
	}
	delete(t.index, id)
	t.size--
	orphans := t.condense(leaf)
	// Root shrink: collapse single-child internal roots.
	for !t.root.leaf() && len(t.root.entries) == 1 {
		t.root = t.root.entries[0].child
		t.root.parent = nil
	}
	// An emptied internal root becomes an empty leaf.
	if !t.root.leaf() && len(t.root.entries) == 0 {
		t.root = &node{level: 0}
	}
	for _, o := range orphans {
		t.insertEntry(o.e, o.level)
	}
	return nil
}

// Replace atomically swaps the rectangle of id. If id does not exist the
// tree is left completely unchanged and ErrNotFound is returned.
func (t *Tree) Replace(id int64, r Rect) error {
	if !r.normalized() {
		return fmt.Errorf("rtree: non-normalized rect %+v", r)
	}
	if _, ok := t.index[id]; !ok {
		return ErrNotFound
	}
	if err := t.Delete(id); err != nil {
		return err
	}
	return t.Insert(id, r)
}

// RangeQuery returns the sorted IDs of all objects intersecting r, plus the
// number of tree nodes visited (a measure of pruning effectiveness).
func (t *Tree) RangeQuery(r Rect) (ids []int64, visited int) {
	ids = []int64{}
	stack := []*node{t.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++
		for _, e := range n.entries {
			if !e.rect.Intersects(r) {
				continue
			}
			if n.leaf() {
				ids = append(ids, e.id)
			} else {
				stack = append(stack, e.child)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, visited
}

// insertEntry inserts e at the given level (0 = leaf objects).
func (t *Tree) insertEntry(e entry, level int) {
	n := t.chooseNode(e.rect, level)
	n.entries = append(n.entries, e)
	if e.child != nil {
		e.child.parent = n
	}
	if n.leaf() {
		t.index[e.id] = n
	}
	t.adjust(n)
}

// chooseNode descends from the root to the target level, always following
// the child whose MBR needs the least enlargement (ties: smaller area).
func (t *Tree) chooseNode(r Rect, level int) *node {
	n := t.root
	for n.level > level {
		best := -1
		var bestEnlarge, bestArea float64
		for i := range n.entries {
			er := n.entries[i].rect
			enl := unionRect(er, r).area() - er.area()
			a := er.area()
			if best < 0 || enl < bestEnlarge || (enl == bestEnlarge && a < bestArea) {
				best, bestEnlarge, bestArea = i, enl, a
			}
		}
		n = n.entries[best].child
	}
	return n
}

// adjust repairs MBRs and propagates splits from n up to the root.
func (t *Tree) adjust(n *node) {
	for n != t.root {
		p := n.parent
		i := childIndex(p, n)
		if len(n.entries) > t.capacity {
			nn := t.splitNode(n)
			p.entries[i].rect = mbrOf(n.entries)
			nn.parent = p
			p.entries = append(p.entries, entry{rect: mbrOf(nn.entries), child: nn})
		} else {
			p.entries[i].rect = mbrOf(n.entries)
		}
		n = p
	}
	// Root split: grow the tree one level.
	if len(t.root.entries) > t.capacity {
		nn := t.splitNode(t.root)
		newRoot := &node{level: t.root.level + 1}
		t.root.parent = newRoot
		nn.parent = newRoot
		newRoot.entries = []entry{
			{rect: mbrOf(t.root.entries), child: t.root},
			{rect: mbrOf(nn.entries), child: nn},
		}
		t.root = newRoot
	}
}

// splitNode performs a quadratic split of n. n keeps one group; the new
// node holding the other group (same level, same parent) is returned.
func (t *Tree) splitNode(n *node) *node {
	all := n.entries
	i, j := pickSeeds(all)
	g1 := []entry{all[i]}
	g2 := []entry{all[j]}
	r1, r2 := all[i].rect, all[j].rect
	rest := make([]entry, 0, len(all)-2)
	for k, e := range all {
		if k != i && k != j {
			rest = append(rest, e)
		}
	}
	for len(rest) > 0 {
		// Guarantee minimum fill: dump the remainder into a short group.
		if len(g1)+len(rest) <= t.minFill {
			g1 = append(g1, rest...)
			break
		}
		if len(g2)+len(rest) <= t.minFill {
			g2 = append(g2, rest...)
			break
		}
		// Pick the entry with the strongest group preference.
		bestK := 0
		bestDiff := -1.0
		for k := range rest {
			d1 := unionRect(r1, rest[k].rect).area() - r1.area()
			d2 := unionRect(r2, rest[k].rect).area() - r2.area()
			diff := d1 - d2
			if diff < 0 {
				diff = -diff
			}
			if diff > bestDiff {
				bestDiff, bestK = diff, k
			}
		}
		e := rest[bestK]
		rest = append(rest[:bestK], rest[bestK+1:]...)
		d1 := unionRect(r1, e.rect).area() - r1.area()
		d2 := unionRect(r2, e.rect).area() - r2.area()
		preferG1 := d1 < d2 ||
			(d1 == d2 && (r1.area() < r2.area() ||
				(r1.area() == r2.area() && len(g1) <= len(g2))))
		if preferG1 {
			g1 = append(g1, e)
			r1 = unionRect(r1, e.rect)
		} else {
			g2 = append(g2, e)
			r2 = unionRect(r2, e.rect)
		}
	}
	n.entries = g1
	nn := &node{level: n.level, parent: n.parent, entries: g2}
	if n.leaf() {
		for k := range g2 {
			t.index[g2[k].id] = nn
		}
	} else {
		for k := range g2 {
			g2[k].child.parent = nn
		}
	}
	return nn
}

// pickSeeds returns the pair of entries wasting the most space if grouped.
func pickSeeds(all []entry) (int, int) {
	bi, bj := 0, 1
	worst := -1.0
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			d := unionRect(all[i].rect, all[j].rect).area() -
				all[i].rect.area() - all[j].rect.area()
			if d > worst {
				worst, bi, bj = d, i, j
			}
		}
	}
	return bi, bj
}

type orphan struct {
	e     entry
	level int
}

// condense walks from n to the root, detaching underfull nodes (their
// entries become orphans for re-insertion) and tightening MBRs otherwise.
func (t *Tree) condense(n *node) []orphan {
	var orphans []orphan
	for n != t.root {
		p := n.parent
		i := childIndex(p, n)
		if len(n.entries) < t.minFill {
			p.entries = append(p.entries[:i], p.entries[i+1:]...)
			for _, e := range n.entries {
				orphans = append(orphans, orphan{e: e, level: n.level})
			}
			n.parent = nil
		} else {
			p.entries[i].rect = mbrOf(n.entries)
		}
		n = p
	}
	return orphans
}

func childIndex(p *node, c *node) int {
	for i := range p.entries {
		if p.entries[i].child == c {
			return i
		}
	}
	return -1
}
