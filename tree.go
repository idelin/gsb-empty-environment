// Package bplustree implements a B+ tree with deletion rebalancing
// that borrows entries from sibling leaves on underflow.
package bplustree

import (
	"fmt"
	"sort"
	"strings"
)

// node is either an internal routing node or a leaf data node.
// Internal node: len(children) == len(keys)+1, keys[i] is the smallest
// key of the subtree rooted at children[i+1].
// Leaf node: keys are the stored records, next links to the right sibling.
type node struct {
	leaf     bool
	keys     []int
	children []*node
	next     *node
	parent   *node
}

// Tree is a B+ tree of a fixed order.
type Tree struct {
	root  *node
	order int // M: max number of keys per node
	min   int // min fill factor: M/2 keys
	size  int
}

// New creates an empty B+ tree of the given order (max keys per node).
// The minimum fill factor is order/2; a non-root node underflows when it
// drops below that many keys.
func New(order int) *Tree {
	if order < 3 {
		order = 3
	}
	return &Tree{
		root:  &node{leaf: true},
		order: order,
		min:   order / 2,
	}
}

// Size returns the number of stored keys.
func (t *Tree) Size() int { return t.size }

// Height returns the number of levels (a tree with only a root leaf is 1).
func (t *Tree) Height() int {
	h := 0
	for n := t.root; n != nil; n = firstChild(n) {
		h++
	}
	return h
}

func firstChild(n *node) *node {
	if n.leaf {
		return nil
	}
	return n.children[0]
}

// Get reports whether key exists in the tree.
func (t *Tree) Get(key int) bool {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.keys, key)
	return i < len(leaf.keys) && leaf.keys[i] == key
}

func (t *Tree) findLeaf(key int) *node {
	n := t.root
	for !n.leaf {
		i := sort.Search(len(n.keys), func(i int) bool { return key < n.keys[i] })
		n = n.children[i]
	}
	return n
}

func indexOfChild(parent, child *node) int {
	for i, c := range parent.children {
		if c == child {
			return i
		}
	}
	panic("child not found in parent")
}

// Insert adds key to the tree; duplicates are ignored.
func (t *Tree) Insert(key int) {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.keys, key)
	if i < len(leaf.keys) && leaf.keys[i] == key {
		return
	}
	leaf.keys = append(leaf.keys, 0)
	copy(leaf.keys[i+1:], leaf.keys[i:])
	leaf.keys[i] = key
	t.size++
	if len(leaf.keys) > t.order {
		t.splitLeaf(leaf)
	}
}

func (t *Tree) splitLeaf(leaf *node) {
	mid := (len(leaf.keys) + 1) / 2
	right := &node{leaf: true, parent: leaf.parent, next: leaf.next}
	right.keys = append(right.keys, leaf.keys[mid:]...)
	leaf.keys = leaf.keys[:mid]
	leaf.next = right
	t.insertIntoParent(leaf, right.keys[0], right)
}

func (t *Tree) insertIntoParent(left *node, key int, right *node) {
	if left == t.root {
		t.root = &node{keys: []int{key}, children: []*node{left, right}}
		left.parent = t.root
		right.parent = t.root
		return
	}
	parent := left.parent
	i := indexOfChild(parent, left)
	parent.keys = append(parent.keys, 0)
	copy(parent.keys[i+1:], parent.keys[i:])
	parent.keys[i] = key
	parent.children = append(parent.children, nil)
	copy(parent.children[i+2:], parent.children[i+1:])
	parent.children[i+1] = right
	right.parent = parent
	if len(parent.keys) > t.order {
		t.splitInternal(parent)
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

// Delete removes key from the tree and rebalances on underflow,
// borrowing from a sibling leaf whenever possible.
// It reports whether the key was present.
func (t *Tree) Delete(key int) bool {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.keys, key)
	if i >= len(leaf.keys) || leaf.keys[i] != key {
		return false
	}
	leaf.keys = append(leaf.keys[:i], leaf.keys[i+1:]...)
	t.size--
	if leaf == t.root {
		return true
	}
	// The smallest key of the leaf changed: fix routing keys up the path.
	if i == 0 && len(leaf.keys) > 0 {
		t.updateAncestors(leaf)
	}
	if len(leaf.keys) < t.min {
		t.rebalanceLeaf(leaf)
	}
	return true
}

// updateAncestors propagates a changed minimum key of n's subtree to the
// nearest ancestor routing key that separates it from its left neighbor.
func (t *Tree) updateAncestors(n *node) {
	key := n.keys[0]
	for n.parent != nil {
		p := n.parent
		if idx := indexOfChild(p, n); idx > 0 {
			p.keys[idx-1] = key
			return
		}
		n = p
	}
}

// rebalanceLeaf fixes a leaf that dropped below the minimum fill factor.
// It first tries to borrow one entry from the left sibling (its maximum
// key), then from the right sibling (its minimum key); only when both
// siblings are at the minimum does it merge.
func (t *Tree) rebalanceLeaf(leaf *node) {
	parent := leaf.parent
	idx := indexOfChild(parent, leaf)

	// Borrow the maximum key from the left sibling.
	if idx > 0 {
		if left := parent.children[idx-1]; len(left.keys) > t.min {
			borrowed := left.keys[len(left.keys)-1]
			left.keys = left.keys[:len(left.keys)-1]
			leaf.keys = append([]int{borrowed}, leaf.keys...)
			// The separator between left and leaf is now the borrowed key.
			parent.keys[idx-1] = leaf.keys[0]
			return
		}
	}

	// Borrow the minimum key from the right sibling.
	if idx < len(parent.children)-1 {
		if right := parent.children[idx+1]; len(right.keys) > t.min {
			leaf.keys = append(leaf.keys, right.keys[0])
			right.keys = right.keys[1:]
			// The separator between leaf and right is right's new minimum.
			parent.keys[idx] = right.keys[0]
			if idx > 0 {
				// leaf was empty: its minimum changed too.
				parent.keys[idx-1] = leaf.keys[0]
			} else {
				t.updateAncestors(leaf)
			}
			return
		}
	}

	// Both siblings are at the minimum: merge.
	if idx > 0 {
		left := parent.children[idx-1]
		left.keys = append(left.keys, leaf.keys...)
		left.next = leaf.next
		removeChild(parent, idx)
	} else {
		right := parent.children[idx+1]
		leaf.keys = append(leaf.keys, right.keys...)
		leaf.next = right.next
		removeChild(parent, idx+1)
		t.updateAncestors(leaf)
	}
	t.rebalanceInternal(parent)
}

func removeChild(parent *node, idx int) {
	parent.keys = append(parent.keys[:idx-1], parent.keys[idx:]...)
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)
}

// rebalanceInternal fixes an internal node that dropped below the minimum
// fill factor after a child removal, shrinking the root if it emptied.
func (t *Tree) rebalanceInternal(n *node) {
	if n == t.root {
		if !n.leaf && len(n.children) == 1 {
			t.root = n.children[0]
			t.root.parent = nil
		}
		return
	}
	if len(n.keys) >= t.min {
		return
	}
	parent := n.parent
	idx := indexOfChild(parent, n)

	if idx > 0 {
		if left := parent.children[idx-1]; len(left.keys) > t.min {
			n.keys = append([]int{parent.keys[idx-1]}, n.keys...)
			parent.keys[idx-1] = left.keys[len(left.keys)-1]
			left.keys = left.keys[:len(left.keys)-1]
			child := left.children[len(left.children)-1]
			left.children = left.children[:len(left.children)-1]
			n.children = append([]*node{child}, n.children...)
			child.parent = n
			return
		}
	}
	if idx < len(parent.children)-1 {
		if right := parent.children[idx+1]; len(right.keys) > t.min {
			n.keys = append(n.keys, parent.keys[idx])
			parent.keys[idx] = right.keys[0]
			right.keys = right.keys[1:]
			child := right.children[0]
			right.children = right.children[1:]
			n.children = append(n.children, child)
			child.parent = n
			return
		}
	}

	if idx > 0 {
		left := parent.children[idx-1]
		left.keys = append(left.keys, parent.keys[idx-1])
		left.keys = append(left.keys, n.keys...)
		for _, c := range n.children {
			c.parent = left
		}
		left.children = append(left.children, n.children...)
		removeChild(parent, idx)
	} else {
		right := parent.children[idx+1]
		n.keys = append(n.keys, parent.keys[idx])
		n.keys = append(n.keys, right.keys...)
		for _, c := range right.children {
			c.parent = n
		}
		n.children = append(n.children, right.children...)
		removeChild(parent, idx+1)
	}
	t.rebalanceInternal(parent)
}

// Keys returns all stored keys in sorted order via the leaf chain.
func (t *Tree) Keys() []int {
	n := t.root
	for !n.leaf {
		n = n.children[0]
	}
	var out []int
	for ; n != nil; n = n.next {
		out = append(out, n.keys...)
	}
	return out
}

// String renders the tree level by level for inspection.
func (t *Tree) String() string {
	var b strings.Builder
	for level, nodes := 0, []*node{t.root}; len(nodes) > 0; level++ {
		fmt.Fprintf(&b, "L%d:", level)
		var next []*node
		for _, n := range nodes {
			fmt.Fprintf(&b, " %v", n.keys)
			if !n.leaf {
				next = append(next, n.children...)
			}
		}
		b.WriteByte('\n')
		nodes = next
	}
	return b.String()
}
