// Package bptree implements an in-memory, page-based B+ tree.
//
// Deletion performs redistribution (borrowing) first; when both siblings
// are at the minimum occupancy it performs a physical merge: two pages are
// combined into one, the separator entry is removed from the parent, and
// the emptied page is returned to the free page list. If the parent itself
// underflows the merge cascades upward, and when the root is left with a
// single child the tree shrinks by one level.
package bptree

import "sort"

const (
	// MaxKeys is the maximum number of keys held by a single page
	// (leaf or internal). An internal page with k keys has k+1 children.
	MaxKeys = 8
	// MinKeys is the minimum occupancy of a non-root page before it is
	// considered underflowed and must borrow or merge.
	MinKeys = MaxKeys / 2
)

// page is a single tree node. Leaf pages are linked through next.
type page struct {
	id       uint32
	leaf     bool
	keys     []int
	children []uint32 // internal pages only: len(keys)+1 entries
	next     uint32   // leaf pages only: right sibling, 0 = none
}

// pager owns all pages and recycles freed ones through a free page list.
type pager struct {
	pages    map[uint32]*page // live pages
	freeList []uint32         // ids of reclaimed pages, reused by alloc
	nextID   uint32
	frees    int // total number of pages ever returned to the free list
}

func newPager() *pager {
	return &pager{pages: make(map[uint32]*page), nextID: 1}
}

// alloc returns a zeroed page, preferring one recycled from the free list.
func (p *pager) alloc(leaf bool) *page {
	if n := len(p.freeList); n > 0 {
		id := p.freeList[n-1]
		p.freeList = p.freeList[:n-1]
		pg := &page{id: id, leaf: leaf}
		p.pages[id] = pg
		return pg
	}
	id := p.nextID
	p.nextID++
	pg := &page{id: id, leaf: leaf}
	p.pages[id] = pg
	return pg
}

// free releases a page back to the free page list.
func (p *pager) free(id uint32) {
	delete(p.pages, id)
	p.freeList = append(p.freeList, id)
	p.frees++
}

func (p *pager) load(id uint32) *page { return p.pages[id] }

// Tree is a B+ tree keyed by int.
type Tree struct {
	p    *pager
	root uint32
	size int
}

// New returns an empty tree whose root is a single leaf page.
func New() *Tree {
	p := newPager()
	root := p.alloc(true)
	return &Tree{p: p, root: root.id}
}

// Size returns the number of keys in the tree.
func (t *Tree) Size() int { return t.size }

// Height returns the number of levels; a tree whose root is a leaf has height 1.
func (t *Tree) Height() int {
	h := 1
	n := t.p.load(t.root)
	for !n.leaf {
		n = t.p.load(n.children[0])
		h++
	}
	return h
}

// Stats reports page usage of the tree.
type Stats struct {
	LivePages  int // pages currently in use by the tree
	FreePages  int // pages sitting in the free page list
	Created    int // distinct pages ever created
	MergedFree int // total pages reclaimed by merges / root shrink
}

// Stats returns current page usage statistics.
func (t *Tree) Stats() Stats {
	return Stats{
		LivePages:  len(t.p.pages),
		FreePages:  len(t.p.freeList),
		Created:    int(t.p.nextID - 1),
		MergedFree: t.p.frees,
	}
}

// Contains reports whether key is present.
func (t *Tree) Contains(key int) bool {
	n := t.p.load(t.root)
	for !n.leaf {
		n = t.p.load(n.children[childIndex(n, key)])
	}
	i := sort.SearchInts(n.keys, key)
	return i < len(n.keys) && n.keys[i] == key
}

// Scan returns all keys in ascending order by walking the leaf chain.
func (t *Tree) Scan() []int {
	out := make([]int, 0, t.size)
	n := t.p.load(t.root)
	for !n.leaf {
		n = t.p.load(n.children[0])
	}
	for {
		out = append(out, n.keys...)
		if n.next == 0 {
			return out
		}
		n = t.p.load(n.next)
	}
}

// childIndex returns the index of the child subtree that may hold key.
func childIndex(n *page, key int) int {
	return sort.Search(len(n.keys), func(i int) bool { return n.keys[i] > key })
}

// Insert adds key to the tree. It returns false if key already exists.
func (t *Tree) Insert(key int) bool {
	root := t.p.load(t.root)
	promoted, newID, split, inserted := t.insert(root, key)
	if !inserted {
		return false
	}
	t.size++
	if split {
		nr := t.p.alloc(false)
		nr.keys = []int{promoted}
		nr.children = []uint32{root.id, newID}
		t.root = nr.id
	}
	return true
}

func (t *Tree) insert(n *page, key int) (promoted int, newID uint32, split, inserted bool) {
	if n.leaf {
		i := sort.SearchInts(n.keys, key)
		if i < len(n.keys) && n.keys[i] == key {
			return 0, 0, false, false
		}
		n.keys = append(n.keys, 0)
		copy(n.keys[i+1:], n.keys[i:])
		n.keys[i] = key
		if len(n.keys) <= MaxKeys {
			return 0, 0, false, true
		}
		mid := len(n.keys) / 2
		right := t.p.alloc(true)
		right.keys = append(right.keys, n.keys[mid:]...)
		n.keys = n.keys[:mid]
		right.next = n.next
		n.next = right.id
		return right.keys[0], right.id, true, true
	}

	i := childIndex(n, key)
	promoted, newID, split, inserted = t.insert(t.p.load(n.children[i]), key)
	if !inserted {
		return 0, 0, false, false
	}
	if split {
		n.keys = append(n.keys, 0)
		copy(n.keys[i+1:], n.keys[i:])
		n.keys[i] = promoted
		n.children = append(n.children, 0)
		copy(n.children[i+2:], n.children[i+1:])
		n.children[i+1] = newID
	}
	if len(n.keys) <= MaxKeys {
		return 0, 0, false, true
	}
	mid := len(n.keys) / 2
	up := n.keys[mid]
	right := t.p.alloc(false)
	right.keys = append(right.keys, n.keys[mid+1:]...)
	right.children = append(right.children, n.children[mid+1:]...)
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	return up, right.id, true, true
}

// Delete removes key from the tree, physically merging pages and shrinking
// the root as needed. It returns false if key is not present.
func (t *Tree) Delete(key int) bool {
	if !t.Contains(key) {
		return false
	}
	t.delete(t.p.load(t.root), key)
	t.size--
	// Shrink the root: an internal root that lost its last separator is
	// replaced by its only child, and its page is reclaimed.
	for {
		root := t.p.load(t.root)
		if root.leaf || len(root.keys) > 0 {
			break
		}
		t.root = root.children[0]
		t.p.free(root.id)
	}
	return true
}

func (t *Tree) delete(n *page, key int) {
	if n.leaf {
		i := sort.SearchInts(n.keys, key)
		n.keys = append(n.keys[:i], n.keys[i+1:]...)
		return
	}
	i := childIndex(n, key)
	child := t.p.load(n.children[i])
	t.delete(child, key)
	if len(child.keys) < MinKeys {
		t.rebalance(n, i)
	}
}

// rebalance fixes an underflowed child of parent at index idx: borrow from a
// sibling when possible, otherwise physically merge with a sibling.
func (t *Tree) rebalance(parent *page, idx int) {
	child := t.p.load(parent.children[idx])
	var left, right *page
	if idx > 0 {
		left = t.p.load(parent.children[idx-1])
	}
	if idx+1 < len(parent.children) {
		right = t.p.load(parent.children[idx+1])
	}

	// Borrow the rightmost entry of the left sibling through the parent.
	if left != nil && len(left.keys) > MinKeys {
		if child.leaf {
			child.keys = append([]int{left.keys[len(left.keys)-1]}, child.keys...)
			left.keys = left.keys[:len(left.keys)-1]
			parent.keys[idx-1] = child.keys[0]
		} else {
			child.keys = append([]int{parent.keys[idx-1]}, child.keys...)
			child.children = append([]uint32{left.children[len(left.children)-1]}, child.children...)
			parent.keys[idx-1] = left.keys[len(left.keys)-1]
			left.keys = left.keys[:len(left.keys)-1]
			left.children = left.children[:len(left.children)-1]
		}
		return
	}

	// Borrow the leftmost entry of the right sibling through the parent.
	if right != nil && len(right.keys) > MinKeys {
		if child.leaf {
			child.keys = append(child.keys, right.keys[0])
			right.keys = right.keys[1:]
			parent.keys[idx] = right.keys[0]
		} else {
			child.keys = append(child.keys, parent.keys[idx])
			child.children = append(child.children, right.children[0])
			parent.keys[idx] = right.keys[0]
			right.keys = right.keys[1:]
			right.children = right.children[1:]
		}
		return
	}

	// Neither sibling can lend: physically merge the two pages into one.
	if left != nil {
		t.merge(parent, idx-1) // merge child into its left sibling
	} else {
		t.merge(parent, idx) // merge the right sibling into child
	}
}

// merge combines the pages at parent.children[sep] and [sep+1] into the left
// one, pulls the separator down from the parent (internal pages), unlinks the
// emptied page and returns it to the free page list.
func (t *Tree) merge(parent *page, sep int) {
	left := t.p.load(parent.children[sep])
	right := t.p.load(parent.children[sep+1])
	if left.leaf {
		left.keys = append(left.keys, right.keys...)
		left.next = right.next
	} else {
		left.keys = append(left.keys, parent.keys[sep])
		left.keys = append(left.keys, right.keys...)
		left.children = append(left.children, right.children...)
	}
	parent.keys = append(parent.keys[:sep], parent.keys[sep+1:]...)
	parent.children = append(parent.children[:sep+1], parent.children[sep+2:]...)
	t.p.free(right.id)
}
