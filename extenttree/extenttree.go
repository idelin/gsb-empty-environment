// Package extenttree implements a filesystem block-mapping extent tree.
//
// The tree is a balanced B+ tree keyed by logical block address (LBA). Each
// leaf stores up to M ordered, non-overlapping extents. Every extent maps a
// half-open logical block range [LBA, LBA+Length) to the physical block range
// [PBA, PBA+Length). An extent may be marked unwritten: reads of an unwritten
// extent must return zeroes instead of stale disk contents.
//
// Adjacent extents are merged immediately when, and only when, they are
// logically adjacent, physically contiguous and carry the same unwritten
// flag. Holes (logical blocks not covered by any extent) are represented by
// the absence of an extent.
//
// Mutations edit the ordered extent set and then rebuild a balanced B+ tree
// bottom-up, so the balance and occupancy invariants hold after every
// operation by construction.
package extenttree

import (
	"errors"
	"sort"
	"sync"
)

// Extent is a single leaf entry: a mapping of [LBA, LBA+Length) logical
// blocks onto [PBA, PBA+Length) physical blocks.
type Extent struct {
	LBA       uint64 // first logical block
	Length    uint64 // number of blocks (always > 0)
	PBA       uint64 // first physical block
	Unwritten bool   // true: allocated but not initialized (read as zero)
}

// End returns the exclusive end logical block of the extent.
func (e Extent) End() uint64 { return e.LBA + e.Length }

// PhysEnd returns the exclusive end physical block of the extent.
func (e Extent) PhysEnd() uint64 { return e.PBA + e.Length }

// PhysRange is a half-open physical block interval [Start, End).
type PhysRange struct {
	Start uint64
	End   uint64
}

// Segment is one element of a Query result. Holes are reported with
// Allocated == false; allocated segments carry PBA and the unwritten flag.
type Segment struct {
	LBA       uint64
	Length    uint64
	PBA       uint64
	Unwritten bool
	Allocated bool
}

// Allocator supplies a physical start block for length contiguous blocks when
// WriteInit encounters an unallocated hole.
type Allocator func(length uint64) (pba uint64, err error)

// Tree is an extent B+ tree. A Tree is safe for concurrent use.
type Tree struct {
	mu sync.Mutex

	m       int // maximum entries per leaf
	minLeaf int // minimum entries per non-root leaf: ceil(m/2)

	root *node
}

type node struct {
	leaf bool

	// Leaf data.
	extents []Extent

	// Internal data: keys[i] is the minimum LBA reachable via children[i+1].
	keys     []uint64
	children []*node

	parent *node
	next   *node // leaf chain, ascending LBA
	prev   *node
}

// New creates a tree whose leaves hold at most m extents. m must be >= 3 so
// that the occupancy invariant [ceil(m/2), m] is maintainable.
func New(m int) (*Tree, error) {
	if m < 3 {
		return nil, errors.New("extenttree: M must be >= 3")
	}
	t := &Tree{
		m:       m,
		minLeaf: (m + 1) / 2,
	}
	t.root = &node{leaf: true}
	return t, nil
}

// M returns the configured maximum leaf occupancy.
func (t *Tree) M() int { return t.m }

// Height returns the number of tree levels (a root-only leaf has height 1).
func (t *Tree) Height() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := 1
	for n := t.root; !n.leaf; n = n.children[0] {
		h++
	}
	return h
}

// Preallocate marks [lba, lba+length) as allocated but unwritten, backed by
// the physical blocks [pba, pba+length). Reads of the range return zeroes.
// The whole logical range must currently be a hole.
func (t *Tree) Preallocate(lba, length, pba uint64) error {
	if length == 0 {
		return errors.New("extenttree: length must be > 0")
	}
	if addOverflow(lba, length) || addOverflow(pba, length) {
		return errors.New("extenttree: range overflow")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.collectHits(lba, lba+length)) > 0 {
		return errors.New("extenttree: Preallocate range overlaps an existing extent")
	}
	nextPBA := pba
	_, err := t.mutate(lba, length, lba+length, modePrealloc, &nextPBA, nil)
	return err
}

// WriteInit ensures every block in [lba, lba+length) is allocated and
// initialized. Allocated unwritten blocks become initialized while keeping
// their physical addresses; unallocated holes receive physical blocks from
// alloc (one contiguous allocation per maximal hole). Already initialized
// blocks are left untouched.
func (t *Tree) WriteInit(lba, length uint64, alloc Allocator) error {
	if length == 0 {
		return errors.New("extenttree: length must be > 0")
	}
	if addOverflow(lba, length) {
		return errors.New("extenttree: range overflow")
	}
	if alloc == nil {
		return errors.New("extenttree: WriteInit requires an allocator")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	_, err := t.mutate(lba, length, lba+length, modeWrite, nil, alloc)
	return err
}

// PunchHole deallocates every allocated block in [lba, lba+length), splitting
// extents as needed, and returns the precise list of released physical
// half-open intervals. Adjacent released intervals are coalesced.
func (t *Tree) PunchHole(lba, length uint64) ([]PhysRange, error) {
	if length == 0 {
		return nil, errors.New("extenttree: length must be > 0")
	}
	if addOverflow(lba, length) {
		return nil, errors.New("extenttree: range overflow")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.mutate(lba, length, lba+length, modePunch, nil, nil)
}

// ZeroRange converts every allocated block in [lba, lba+length) to unwritten
// while retaining its physical blocks. Holes are left untouched.
func (t *Tree) ZeroRange(lba, length uint64) error {
	if length == 0 {
		return errors.New("extenttree: length must be > 0")
	}
	if addOverflow(lba, length) {
		return errors.New("extenttree: range overflow")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	_, err := t.mutate(lba, length, lba+length, modeZero, nil, nil)
	return err
}

// Query reports the mapping of every block in [lba, lba+length), including
// hole segments, in ascending LBA order.
func (t *Tree) Query(lba, length uint64) ([]Segment, error) {
	if length == 0 {
		return nil, errors.New("extenttree: length must be > 0")
	}
	if addOverflow(lba, length) {
		return nil, errors.New("extenttree: range overflow")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	end := lba + length
	var segs []Segment
	cursor := lba

	for n := t.findLeaf(lba); n != nil; n = n.next {
		for _, e := range n.extents {
			if e.LBA >= end {
				return segs, nil
			}
			if cursor < e.LBA {
				holeEnd := min(e.LBA, end)
				segs = append(segs, Segment{LBA: cursor, Length: holeEnd - cursor})
				cursor = holeEnd
				if cursor == end {
					return segs, nil
				}
			}
			if e.End() > lba {
				segStart := max(e.LBA, lba)
				segEnd := min(e.End(), end)
				segs = append(segs, Segment{
					LBA:       segStart,
					Length:    segEnd - segStart,
					PBA:       e.PBA + segStart - e.LBA,
					Unwritten: e.Unwritten,
					Allocated: true,
				})
				cursor = segEnd
				if cursor == end {
					return segs, nil
				}
			}
		}
	}
	if cursor < end {
		segs = append(segs, Segment{LBA: cursor, Length: end - cursor})
	}
	return segs, nil
}

func addOverflow(base, length uint64) bool {
	return base+length < base
}

type mode int

const (
	modePrealloc mode = iota
	modeWrite
	modePunch
	modeZero
)

// mutate computes the replacement extents for [lba, end) from the current
// ordered extent set, applies the requested transformation, and rebuilds the
// B+ tree. Punch operations return the released physical half-open intervals.
func (t *Tree) mutate(lba, length, end uint64, m mode, preallocPBA *uint64, alloc Allocator) ([]PhysRange, error) {
	hits := t.collectHits(lba, end)

	var outputs []Extent
	var released []PhysRange
	cursor := lba

	flushHole := func(holeStart, holeLen uint64) error {
		switch m {
		case modeWrite:
			pba, err := alloc(holeLen)
			if err != nil {
				return err
			}
			if addOverflow(pba, holeLen) {
				return errors.New("extenttree: allocated physical range overflow")
			}
			outputs = append(outputs, Extent{LBA: holeStart, Length: holeLen, PBA: pba})
		case modePrealloc:
			pba := *preallocPBA
			outputs = append(outputs, Extent{LBA: holeStart, Length: holeLen, PBA: pba, Unwritten: true})
			*preallocPBA = pba + holeLen
		case modePunch, modeZero:
			// Holes stay holes: nothing to map.
		}
		return nil
	}

	for _, h := range hits {
		hitStart := max(h.LBA, lba)
		hitEnd := min(h.End(), end)

		if cursor < hitStart {
			if err := flushHole(cursor, hitStart-cursor); err != nil {
				return nil, err
			}
		}

		// Unchanged head before the edited range.
		if h.LBA < lba {
			outputs = append(outputs, Extent{
				LBA:       h.LBA,
				Length:    lba - h.LBA,
				PBA:       h.PBA,
				Unwritten: h.Unwritten,
			})
		}

		midLen := hitEnd - hitStart
		midPBA := h.PBA + (hitStart - h.LBA)
		switch m {
		case modeWrite:
			outputs = append(outputs, Extent{LBA: hitStart, Length: midLen, PBA: midPBA})
		case modeZero:
			outputs = append(outputs, Extent{LBA: hitStart, Length: midLen, PBA: midPBA, Unwritten: true})
		case modePunch:
			released = append(released, PhysRange{Start: midPBA, End: midPBA + midLen})
		case modePrealloc:
			return nil, errors.New("extenttree: internal error: preallocate over existing extent")
		}

		// Unchanged tail after the edited range.
		if h.End() > end {
			tailOff := end - h.LBA
			outputs = append(outputs, Extent{
				LBA:       end,
				Length:    h.End() - end,
				PBA:       h.PBA + tailOff,
				Unwritten: h.Unwritten,
			})
		}

		cursor = hitEnd
	}

	if cursor < end {
		if err := flushHole(cursor, end-cursor); err != nil {
			return nil, err
		}
	}

	// Build the new full ordered extent set: unaffected extents plus the
	// replacement outputs, then coalesce every compatible adjacent pair.
	merged := make([]Extent, 0, len(t.allExtents())+len(outputs))
	merged = append(merged, t.allExtentsExcept(hits)...)
	merged = append(merged, outputs...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].LBA < merged[j].LBA })
	merged = coalesceExtents(merged)

	t.rebuild(merged)

	if m == modePunch {
		released = coalescePhys(released)
	}
	return released, nil
}

// allExtents returns every extent in ascending LBA order via the leaf chain.
func (t *Tree) allExtents() []Extent {
	var out []Extent
	for n := t.leftmostLeaf(); n != nil; n = n.next {
		out = append(out, n.extents...)
	}
	return out
}

// allExtentsExcept returns every extent not identity-equal to one of hits.
func (t *Tree) allExtentsExcept(hits []Extent) []Extent {
	skip := map[Extent]bool{}
	for _, h := range hits {
		skip[h] = true
	}
	var out []Extent
	for _, e := range t.allExtents() {
		if !skip[e] {
			out = append(out, e)
		}
	}
	return out
}

// collectHits returns every extent intersecting [lba, end), ascending.
func (t *Tree) collectHits(lba, end uint64) []Extent {
	var hits []Extent
	for n := t.findLeaf(lba); n != nil; n = n.next {
		for _, e := range n.extents {
			if e.LBA >= end {
				return hits
			}
			if e.End() > lba {
				hits = append(hits, e)
			}
		}
	}
	return hits
}

// canMerge reports whether two logically adjacent extents may coalesce.
func canMerge(a, b Extent) bool {
	return a.End() == b.LBA && a.PhysEnd() == b.PBA && a.Unwritten == b.Unwritten
}

// coalesceExtents folds adjacent, physically contiguous, same-flag extents.
// Extents that differ in flag or are physically discontiguous stay separate.
func coalesceExtents(in []Extent) []Extent {
	if len(in) == 0 {
		return nil
	}
	out := []Extent{in[0]}
	for _, e := range in[1:] {
		last := &out[len(out)-1]
		if canMerge(*last, e) {
			last.Length += e.Length
		} else {
			out = append(out, e)
		}
	}
	return out
}

// coalescePhys merges adjacent (touching) physical intervals into maximal ones.
func coalescePhys(in []PhysRange) []PhysRange {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	out := []PhysRange{in[0]}
	for _, r := range in[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
		} else {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// B+ tree navigation and bulk rebuild
// ---------------------------------------------------------------------------

func (t *Tree) findLeaf(lba uint64) *node {
	n := t.root
	for !n.leaf {
		i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] > lba })
		n = n.children[i]
	}
	return n
}

func (t *Tree) leftmostLeaf() *node {
	n := t.root
	for !n.leaf {
		n = n.children[0]
	}
	return n
}

// rebuild constructs a fresh balanced B+ tree from an ordered, already
// coalesced extent set. The height is chosen up front so the extents can be
// partitioned into a number of leaves that yields valid occupancy at every
// level; all non-root nodes therefore satisfy [minLeaf, m] by construction.
func (t *Tree) rebuild(extents []Extent) {
	if len(extents) == 0 {
		t.root = &node{leaf: true}
		return
	}

	minLeaf := t.minLeaf
	maxLeaf := t.m
	n := len(extents)

	// Choose the leaf count first: it must permit leaf sizes within
	// [minLeaf, maxLeaf] and a valid internal tree above it. Search counts in
	// ascending order and pick the first fully feasible one; this yields the
	// smallest balanced tree that satisfies every occupancy bound.
	leafCount := 1
	for {
		minOK := n/leafCount >= minLeaf || leafCount == 1
		maxOK := (n+leafCount-1)/leafCount <= maxLeaf
		if minOK && maxOK && t.leafCountFeasible(leafCount) {
			break
		}
		leafCount++
	}

	sizes := balancedSizes(n, leafCount, minLeaf, maxLeaf)
	leaves := make([]*node, 0, leafCount)
	start := 0
	for _, size := range sizes {
		group := append([]Extent(nil), extents[start:start+size]...)
		leaves = append(leaves, &node{leaf: true, extents: group})
		start += size
	}

	for i, lf := range leaves {
		lf.parent = nil
		lf.prev = nil
		lf.next = nil
		if i > 0 {
			leaves[i-1].next = lf
			lf.prev = leaves[i-1]
		}
	}

	t.root = t.buildLevel(leaves)
}

// leafCountFeasible reports whether count leaf nodes can be grouped into
// internal levels down to a single root while every non-root internal node
// keeps between (minLeaf+1) and (m+1) children.
func (t *Tree) leafCountFeasible(count int) bool {
	minChildren := t.minLeaf + 1
	maxChildren := t.m + 1
	for count > 1 {
		groups := bestGroupCount(count, minChildren, maxChildren)
		// A single over-full group means this count cannot form a valid tree.
		if groups == 1 && count > maxChildren {
			return false
		}
		// Every non-root group must reach the minimum child count.
		if groups > 1 && count/groups < minChildren {
			return false
		}
		count = groups
	}
	return true
}

// buildLevel groups child nodes into internal nodes recursively until a single
// root remains. Each group respects [minChildren, maxChildren].
func (t *Tree) buildLevel(children []*node) *node {
	if len(children) == 1 {
		return children[0]
	}
	minChildren := t.minLeaf + 1
	maxChildren := t.m + 1
	groups := bestGroupCount(len(children), minChildren, maxChildren)
	sizes := balancedSizes(len(children), groups, minChildren, maxChildren)

	parents := make([]*node, 0, groups)
	start := 0
	for _, size := range sizes {
		parents = append(parents, t.newInternal(children[start:start+size]))
		start += size
	}
	return t.buildLevel(parents)
}

// bestGroupCount returns a group count whose groups all fit within
// [minSize, maxSize], preferring fewer, fuller groups.
func bestGroupCount(n, minSize, maxSize int) int {
	if n <= maxSize {
		return 1
	}
	groups := (n + maxSize - 1) / maxSize
	for n/groups < minSize {
		groups--
	}
	if groups < 1 {
		groups = 1
	}
	return groups
}

// balancedSizes splits n into exactly groups parts, each within
// [minSize, maxSize], differing by at most one and ordered largest first.
func balancedSizes(n, groups, minSize, maxSize int) []int {
	base := n / groups
	extra := n % groups
	sizes := make([]int, groups)
	for i := range sizes {
		sizes[i] = base
		if i < extra {
			sizes[i]++
		}
	}
	return sizes
}

// newInternal builds an internal node over the given children, wiring parent
// pointers and separator keys from each child's subtree minimum.
func (t *Tree) newInternal(children []*node) *node {
	n := &node{leaf: false, children: children}
	for i, c := range children {
		c.parent = n
		if i > 0 {
			n.keys = append(n.keys, subtreeMin(c))
		}
	}
	return n
}

func subtreeMin(n *node) uint64 {
	for !n.leaf {
		n = n.children[0]
	}
	return n.extents[0].LBA
}
