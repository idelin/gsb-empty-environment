// Package cowtree implements a B+ tree with page-level Copy-On-Write.
//
// Design:
//   - Nodes ("pages") are immutable once published. A write transaction never
//     mutates a node in place; it copies the nodes along the path from the
//     root to the target leaf, applies the modification to the copies, and
//     finally swaps the root pointer atomically.
//   - Read-only transactions (Snapshot) simply capture the current root
//     pointer. Because every page reachable from that root is immutable, the
//     snapshot traverses a consistent historical version with zero locking,
//     even while a concurrent writer is splitting nodes and publishing new
//     versions of the tree.
package cowtree

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxEntries is the maximum number of keys a page may hold before it splits.
// Kept small so tests exercise splits heavily; a real store would use a
// page-size-derived value (e.g. hundreds of entries per 4KB page).
const MaxEntries = 8

// node is a B+ tree page. Implementations are immutable after construction.
type node interface{ isNode() }

// leaf is a leaf page holding sorted keys and their values.
type leaf struct {
	keys []int
	vals []string
}

// internal is an internal page: len(keys) == len(child)-1, and child[i]
// holds keys k with keys[i-1] <= k < keys[i].
type internal struct {
	keys  []int
	child []node
}

func (*leaf) isNode()     {}
func (*internal) isNode() {}

// rootRef boxes the root so the atomic pointer has a single concrete type.
type rootRef struct{ n node }

// Tree is a CoW B+ tree. It supports a single concurrent writer and any
// number of lock-free readers/snapshots.
type Tree struct {
	root atomic.Pointer[rootRef]
	wmu  sync.Mutex // serializes write transactions
}

// New returns an empty tree.
func New() *Tree {
	t := &Tree{}
	t.root.Store(&rootRef{n: &leaf{}})
	return t
}

// split describes a page that overflowed: right is the new sibling page and
// sep is the separator key to insert into the parent.
type split struct {
	sep   int
	right node
}

// insert returns the page that should replace n, plus split information if n
// overflowed. n itself is never modified.
func insert(n node, key int, val string) (node, *split) {
	switch x := n.(type) {
	case *leaf:
		i := sort.Search(len(x.keys), func(i int) bool { return x.keys[i] >= key })
		keys := append([]int(nil), x.keys...)
		vals := append([]string(nil), x.vals...)
		if i < len(keys) && keys[i] == key {
			vals[i] = val // overwrite: copy-on-write, original page untouched
		} else {
			keys = insertInt(keys, i, key)
			vals = insertStr(vals, i, val)
		}
		if len(keys) <= MaxEntries {
			return &leaf{keys: keys, vals: vals}, nil
		}
		mid := len(keys) / 2
		left := &leaf{keys: keys[:mid:mid], vals: vals[:mid:mid]}
		right := &leaf{keys: keys[mid:], vals: vals[mid:]}
		return left, &split{sep: keys[mid], right: right}

	case *internal:
		i := sort.Search(len(x.keys), func(i int) bool { return x.keys[i] > key })
		child, sp := insert(x.child[i], key, val)
		keys := append([]int(nil), x.keys...)
		childs := append([]node(nil), x.child...)
		childs[i] = child
		if sp != nil {
			keys = insertInt(keys, i, sp.sep)
			childs = insertNode(childs, i+1, sp.right)
		}
		if len(keys) <= MaxEntries {
			return &internal{keys: keys, child: childs}, nil
		}
		mid := len(keys) / 2
		left := &internal{keys: keys[:mid], child: childs[:mid+1]}
		right := &internal{keys: keys[mid+1:], child: childs[mid+1:]}
		return left, &split{sep: keys[mid], right: right}
	}
	panic("unreachable")
}

func insertInt(s []int, i int, v int) []int {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertStr(s []string, i int, v string) []string {
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertNode(s []node, i int, v node) []node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// Put inserts or overwrites key in a single write transaction. The new root
// is published with one atomic store; readers holding the old root are
// unaffected.
func (t *Tree) Put(key int, val string) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	root, sp := insert(t.root.Load().n, key, val)
	if sp != nil {
		root = &internal{keys: []int{sp.sep}, child: []node{root, sp.right}}
	}
	t.root.Store(&rootRef{n: root})
}

// Snapshot is a read-only transaction: a stable, consistent view of the tree
// as of the moment it was taken. It is safe for concurrent use and never
// blocks writers.
type Snapshot struct {
	root node
	seq  uint64
}

// Snapshot captures the current root. O(1), lock-free.
func (t *Tree) Snapshot() *Snapshot {
	return &Snapshot{root: t.root.Load().n}
}

// Get returns the value for key as visible in this snapshot.
func (s *Snapshot) Get(key int) (string, bool) {
	n := s.root
	for {
		switch x := n.(type) {
		case *leaf:
			i := sort.Search(len(x.keys), func(i int) bool { return x.keys[i] >= key })
			if i < len(x.keys) && x.keys[i] == key {
				return x.vals[i], true
			}
			return "", false
		case *internal:
			i := sort.Search(len(x.keys), func(i int) bool { return x.keys[i] > key })
			n = x.child[i]
		}
	}
}

// Scan iterates all key/value pairs in ascending key order. Returning false
// from fn stops the iteration early.
func (s *Snapshot) Scan(fn func(key int, val string) bool) {
	scan(s.root, fn)
}

func scan(n node, fn func(key int, val string) bool) bool {
	switch x := n.(type) {
	case *leaf:
		for i := range x.keys {
			if !fn(x.keys[i], x.vals[i]) {
				return false
			}
		}
		return true
	case *internal:
		for _, c := range x.child {
			if !scan(c, fn) {
				return false
			}
		}
		return true
	}
	panic("unreachable")
}

// Collect returns all key/value pairs visible in this snapshot, in order.
func (s *Snapshot) Collect() ([]int, []string) {
	var keys []int
	var vals []string
	s.Scan(func(k int, v string) bool {
		keys = append(keys, k)
		vals = append(vals, v)
		return true
	})
	return keys, vals
}

// Count returns the number of entries visible in this snapshot.
func (s *Snapshot) Count() int {
	n := 0
	s.Scan(func(int, string) bool { n++; return true })
	return n
}

// Height returns the tree height of this snapshot (a single leaf is 1).
func (s *Snapshot) Height() int {
	h := 0
	for n := s.root; n != nil; {
		h++
		if in, ok := n.(*internal); ok {
			n = in.child[0]
		} else {
			n = nil
		}
	}
	return h
}

// String renders the snapshot's tree structure (for demos/debugging).
func (s *Snapshot) String() string {
	var b strings.Builder
	var walk func(n node, depth int)
	walk = func(n node, depth int) {
		pad := strings.Repeat("  ", depth)
		switch x := n.(type) {
		case *leaf:
			fmt.Fprintf(&b, "%sleaf %v\n", pad, x.keys)
		case *internal:
			fmt.Fprintf(&b, "%sinternal %v\n", pad, x.keys)
			for _, c := range x.child {
				walk(c, depth+1)
			}
		}
	}
	walk(s.root, 0)
	return b.String()
}
