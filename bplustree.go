// Package bplustree implements a B+ tree with insert, delete and search.
// Deletion rebalances underflowed leaf nodes by borrowing from siblings
// (left sibling first) or merging, keeping parent separator keys exact.
package bplustree

import (
	"fmt"
	"sort"
	"strings"
)

// Tree is a B+ tree keyed by int with int values.
type Tree struct {
	root  *node
	order int // max number of keys per node
	size  int
}

type node struct {
	leaf     bool
	keys     []int
	vals     []int   // leaf only
	children []*node // internal only
	next     *node   // leaf level linkage
	parent   *node
}

// New creates an empty B+ tree. order is the max number of keys per node;
// a node underflows when its key count drops below order/2.
func New(order int) *Tree {
	if order < 3 {
		order = 3
	}
	return &Tree{root: &node{leaf: true}, order: order}
}

// minKeys is the minimum fill factor: a non-root node must hold at least
// order/2 keys.
func (t *Tree) minKeys() int { return t.order / 2 }

// Size returns the number of records in the tree.
func (t *Tree) Size() int { return t.size }

// Height returns the number of levels (root-only tree has height 1).
func (t *Tree) Height() int {
	h := 0
	for n := t.root; n != nil; n = n.children[0] {
		h++
		if n.leaf {
			break
		}
	}
	return h
}

// Get returns the value stored under key.
func (t *Tree) Get(key int) (int, bool) {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.keys, key)
	if i < len(leaf.keys) && leaf.keys[i] == key {
		return leaf.vals[i], true
	}
	return 0, false
}

// Keys returns all keys in ascending order via the leaf chain.
func (t *Tree) Keys() []int {
	out := make([]int, 0, t.size)
	for n := t.leftmost(); n != nil; n = n.next {
		out = append(out, n.keys...)
	}
	return out
}

func (t *Tree) leftmost() *node {
	n := t.root
	for !n.leaf {
		n = n.children[0]
	}
	return n
}

func (t *Tree) findLeaf(key int) *node {
	n := t.root
	for !n.leaf {
		i := sort.SearchInts(n.keys, key)
		if i < len(n.keys) && n.keys[i] == key {
			i++
		}
		n = n.children[i]
	}
	return n
}

// Insert adds or replaces a key/value pair.
func (t *Tree) Insert(key, val int) {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.keys, key)
	if i < len(leaf.keys) && leaf.keys[i] == key {
		leaf.vals[i] = val
		return
	}
	leaf.keys = insertInt(leaf.keys, i, key)
	leaf.vals = insertInt(leaf.vals, i, val)
	t.size++
	if len(leaf.keys) > t.order {
		t.splitLeaf(leaf)
	}
}

func (t *Tree) splitLeaf(leaf *node) {
	mid := len(leaf.keys) / 2
	right := &node{leaf: true, parent: leaf.parent}
	right.keys = append(right.keys, leaf.keys[mid:]...)
	right.vals = append(right.vals, leaf.vals[mid:]...)
	leaf.keys = leaf.keys[:mid]
	leaf.vals = leaf.vals[:mid]
	right.next = leaf.next
	leaf.next = right
	t.insertIntoParent(leaf, right.keys[0], right)
}

func (t *Tree) insertIntoParent(left *node, key int, right *node) {
	if left.parent == nil {
		root := &node{keys: []int{key}, children: []*node{left, right}}
		left.parent = root
		right.parent = root
		t.root = root
		return
	}
	p := left.parent
	i := childIndex(p, left)
	p.keys = insertInt(p.keys, i, key)
	p.children = insertNode(p.children, i+1, right)
	right.parent = p
	if len(p.keys) > t.order {
		t.splitInternal(p)
	}
}

func (t *Tree) splitInternal(n *node) {
	mid := len(n.keys) / 2
	up := n.keys[mid]
	right := &node{parent: n.parent}
	right.keys = append(right.keys, n.keys[mid+1:]...)
	right.children = append(right.children, n.children[mid+1:]...)
	for _, c := range right.children {
		c.parent = right
	}
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	t.insertIntoParent(n, up, right)
}

// Delete removes key. It returns false when the key does not exist.
// On leaf underflow it first tries to borrow one record from the left
// sibling (its maximum key), then from the right sibling (its minimum
// key), and only merges when neither sibling has a surplus.
func (t *Tree) Delete(key int) bool {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.keys, key)
	if i >= len(leaf.keys) || leaf.keys[i] != key {
		return false
	}
	leaf.keys = removeInt(leaf.keys, i)
	leaf.vals = removeInt(leaf.vals, i)
	t.size--

	if leaf == t.root {
		return true
	}
	if len(leaf.keys) >= t.minKeys() {
		if i == 0 {
			// The leaf's minimum changed: fix the separator in an ancestor.
			t.fixSeparator(leaf, leaf.keys[0])
		}
		return true
	}
	t.rebalanceLeaf(leaf)
	return true
}

// fixSeparator walks up until the subtree rooted at n is a non-first
// child, then rewrites the separator to the subtree's new minimum key.
func (t *Tree) fixSeparator(n *node, newMin int) {
	for n.parent != nil {
		p := n.parent
		i := childIndex(p, n)
		if i > 0 {
			p.keys[i-1] = newMin
			return
		}
		n = p
	}
}

func (t *Tree) rebalanceLeaf(leaf *node) {
	p := leaf.parent
	i := childIndex(p, leaf)

	// Borrow from the left sibling: steal its maximum record.
	if i > 0 {
		left := p.children[i-1]
		if len(left.keys) > t.minKeys() {
			leaf.keys = insertInt(leaf.keys, 0, left.keys[len(left.keys)-1])
			leaf.vals = insertInt(leaf.vals, 0, left.vals[len(left.vals)-1])
			left.keys = left.keys[:len(left.keys)-1]
			left.vals = left.vals[:len(left.vals)-1]
			// The routing key between left and leaf is now leaf's minimum.
			p.keys[i-1] = leaf.keys[0]
			return
		}
	}
	// Borrow from the right sibling: steal its minimum record.
	if i < len(p.children)-1 {
		right := p.children[i+1]
		if len(right.keys) > t.minKeys() {
			leaf.keys = append(leaf.keys, right.keys[0])
			leaf.vals = append(leaf.vals, right.vals[0])
			right.keys = right.keys[1:]
			right.vals = right.vals[1:]
			// The routing key between leaf and right is right's new minimum.
			p.keys[i] = right.keys[0]
			// The deleted key may have been the leaf's minimum; if so the
			// separator recording the leaf's minimum is stale.
			t.fixSeparator(leaf, leaf.keys[0])
			return
		}
	}
	// No surplus on either side: merge with a sibling.
	if i > 0 {
		left := p.children[i-1]
		left.keys = append(left.keys, leaf.keys...)
		left.vals = append(left.vals, leaf.vals...)
		left.next = leaf.next
		p.keys = removeInt(p.keys, i-1)
		p.children = removeNode(p.children, i)
	} else {
		right := p.children[1]
		leaf.keys = append(leaf.keys, right.keys...)
		leaf.vals = append(leaf.vals, right.vals...)
		leaf.next = right.next
		p.keys = removeInt(p.keys, 0)
		p.children = removeNode(p.children, 1)
		// The surviving leaf's minimum may have changed.
		t.fixSeparator(leaf, leaf.keys[0])
	}
	t.rebalanceInternal(p)
}

func (t *Tree) rebalanceInternal(n *node) {
	if n == t.root {
		if !n.leaf && len(n.keys) == 0 {
			t.root = n.children[0]
			t.root.parent = nil
		}
		return
	}
	if len(n.keys) >= t.minKeys() {
		return
	}
	p := n.parent
	i := childIndex(p, n)

	if i > 0 {
		left := p.children[i-1]
		if len(left.keys) > t.minKeys() {
			// Pull the separator down, push left's max separator up.
			n.keys = insertInt(n.keys, 0, p.keys[i-1])
			n.children = insertNode(n.children, 0, left.children[len(left.children)-1])
			n.children[0].parent = n
			left.children = left.children[:len(left.children)-1]
			p.keys[i-1] = left.keys[len(left.keys)-1]
			left.keys = left.keys[:len(left.keys)-1]
			return
		}
	}
	if i < len(p.children)-1 {
		right := p.children[i+1]
		if len(right.keys) > t.minKeys() {
			n.keys = append(n.keys, p.keys[i])
			n.children = append(n.children, right.children[0])
			n.children[len(n.children)-1].parent = n
			right.children = right.children[1:]
			p.keys[i] = right.keys[0]
			right.keys = right.keys[1:]
			return
		}
	}
	if i > 0 {
		left := p.children[i-1]
		left.keys = append(left.keys, p.keys[i-1])
		left.keys = append(left.keys, n.keys...)
		left.children = append(left.children, n.children...)
		for _, c := range n.children {
			c.parent = left
		}
		p.keys = removeInt(p.keys, i-1)
		p.children = removeNode(p.children, i)
	} else {
		right := p.children[1]
		n.keys = append(n.keys, p.keys[0])
		n.keys = append(n.keys, right.keys...)
		n.children = append(n.children, right.children...)
		for _, c := range right.children {
			c.parent = n
		}
		p.keys = removeInt(p.keys, 0)
		p.children = removeNode(p.children, 1)
	}
	t.rebalanceInternal(p)
}

// String renders the tree level by level for inspection.
func (t *Tree) String() string {
	var sb strings.Builder
	level := []*node{t.root}
	for depth := 0; len(level) > 0; depth++ {
		fmt.Fprintf(&sb, "L%d:", depth)
		var next []*node
		for _, n := range level {
			fmt.Fprintf(&sb, " %v", n.keys)
			if !n.leaf {
				next = append(next, n.children...)
			}
		}
		sb.WriteString("\n")
		level = next
	}
	return sb.String()
}

func childIndex(p, child *node) int {
	for i, c := range p.children {
		if c == child {
			return i
		}
	}
	panic("child not found in parent")
}

func insertInt(s []int, i, v int) []int {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func removeInt(s []int, i int) []int {
	return append(s[:i], s[i+1:]...)
}

func insertNode(s []*node, i int, n *node) []*node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = n
	return s
}

func removeNode(s []*node, i int) []*node {
	return append(s[:i], s[i+1:]...)
}
