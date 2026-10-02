// Package cowtree implements a B+ tree with lock-free read-only snapshots
// and page-level copy-on-write (CoW).
//
// Design:
//   - Every node is a "page". Once created, a page is immutable.
//   - Writes never modify a page in place. Instead, the writer copies the
//     pages along the path from root to leaf, applies the modification to
//     the copies (allocating fresh page IDs), and atomically swaps the root
//     pointer. Splits allocate brand-new pages as well.
//   - A read-only transaction captures the root pointer at begin time and
//     traverses the old version without any locks. Because old pages are
//     never mutated, the reader observes a consistent snapshot of the table
//     as of the moment the transaction began, even while a concurrent
//     writer is splitting nodes.
package cowtree

const (
	// MaxLeafEntries is the maximum number of key/value pairs in a leaf page.
	MaxLeafEntries = 16
	// MaxInternalKeys is the maximum number of separator keys in an
	// internal page (children = keys + 1).
	MaxInternalKeys = 16
)

// page is an immutable B+ tree node. A page is either a leaf or an
// internal node:
//
//	leaf:     keys[i] -> vals[i]
//	internal: children[i] covers keys < keys[i] (for i == 0) or
//	          keys[i-1] <= k < keys[i]; len(children) == len(keys)+1
//
// Fields must never be mutated after the page is published.
type page struct {
	id       uint64
	leaf     bool
	keys     []int64
	vals     []int64 // leaf only
	children []*page // internal only
}

// allocator hands out fresh page IDs. Every CoW copy or split page gets a
// new ID, which makes "no in-place modification" observable and testable.
type allocator interface{ allocID() uint64 }

// split describes a page that overflowed and was divided in two.
type split struct {
	sep   int64 // separator key pushed up to the parent
	right *page // newly allocated right sibling
}

// insert returns a (possibly new) page that replaces p, plus a split when p
// overflowed. p itself is never modified.
func insert(p *page, key, val int64, a allocator) (*page, *split) {
	if p.leaf {
		return insertLeaf(p, key, val, a)
	}
	return insertInternal(p, key, val, a)
}

func insertLeaf(p *page, key, val int64, a allocator) (*page, *split) {
	i := 0
	for i < len(p.keys) && p.keys[i] < key {
		i++
	}

	keys := make([]int64, len(p.keys), len(p.keys)+1)
	vals := make([]int64, len(p.vals), len(p.vals)+1)
	copy(keys, p.keys)
	copy(vals, p.vals)

	if i < len(p.keys) && p.keys[i] == key {
		// Update: still a copy, never in place.
		vals[i] = val
		return &page{id: a.allocID(), leaf: true, keys: keys, vals: vals}, nil
	}

	keys = append(keys, 0)
	vals = append(vals, 0)
	copy(keys[i+1:], keys[i:])
	copy(vals[i+1:], vals[i:])
	keys[i] = key
	vals[i] = val

	if len(keys) <= MaxLeafEntries {
		return &page{id: a.allocID(), leaf: true, keys: keys, vals: vals}, nil
	}

	// Overflow: split into two freshly allocated leaves.
	mid := len(keys) / 2
	right := &page{
		id:   a.allocID(),
		leaf: true,
		keys: append([]int64(nil), keys[mid:]...),
		vals: append([]int64(nil), vals[mid:]...),
	}
	left := &page{
		id:   a.allocID(),
		leaf: true,
		keys: append([]int64(nil), keys[:mid]...),
		vals: append([]int64(nil), vals[:mid]...),
	}
	return left, &split{sep: right.keys[0], right: right}
}

func insertInternal(p *page, key, val int64, a allocator) (*page, *split) {
	i := 0
	for i < len(p.keys) && key >= p.keys[i] {
		i++
	}

	newChild, sp := insert(p.children[i], key, val, a)

	keys := append([]int64(nil), p.keys...)
	children := append([]*page(nil), p.children...)
	children[i] = newChild
	if sp != nil {
		keys = append(keys, 0)
		copy(keys[i+1:], keys[i:])
		keys[i] = sp.sep
		children = append(children, nil)
		copy(children[i+2:], children[i+1:])
		children[i+1] = sp.right
	}

	if len(keys) <= MaxInternalKeys {
		return &page{id: a.allocID(), keys: keys, children: children}, nil
	}

	// Overflow: split the internal page; the middle separator moves up.
	mid := len(keys) / 2
	right := &page{
		id:       a.allocID(),
		keys:     append([]int64(nil), keys[mid+1:]...),
		children: append([]*page(nil), children[mid+1:]...),
	}
	left := &page{
		id:       a.allocID(),
		keys:     append([]int64(nil), keys[:mid]...),
		children: append([]*page(nil), children[:mid+1]...),
	}
	return left, &split{sep: keys[mid], right: right}
}

// get looks up key starting at p. Lock-free: pages are immutable.
func get(p *page, key int64) (int64, bool) {
	for p != nil {
		if p.leaf {
			i := 0
			for i < len(p.keys) && p.keys[i] < key {
				i++
			}
			if i < len(p.keys) && p.keys[i] == key {
				return p.vals[i], true
			}
			return 0, false
		}
		i := 0
		for i < len(p.keys) && key >= p.keys[i] {
			i++
		}
		p = p.children[i]
	}
	return 0, false
}
