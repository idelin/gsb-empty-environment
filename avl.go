package netsec

import (
	"bytes"
	"sync/atomic"
)

// avlNode is a node of the AVL tree used by an overfull bucket.
// Nodes are ordered by the raw key bytes (bytes.Compare), never by
// hash value, so an attacker who fully breaks the hash still cannot
// force degenerate ordering.
type avlNode struct {
	key    []byte
	value  []byte
	height int
	left   *avlNode
	right  *avlNode
}

// avlTree is a self-balancing binary search tree. Every key comparison
// is charged to cmp so tests can prove logarithmic worst-case behavior.
type avlTree struct {
	root *avlNode
	size int
	cmp  *atomic.Uint64
}

func (t *avlTree) compare(a, b []byte) int {
	t.cmp.Add(1)
	return bytes.Compare(a, b)
}

func nodeHeight(n *avlNode) int {
	if n == nil {
		return 0
	}
	return n.height
}

func updateHeight(n *avlNode) {
	l, r := nodeHeight(n.left), nodeHeight(n.right)
	if l > r {
		n.height = l + 1
	} else {
		n.height = r + 1
	}
}

func balanceFactor(n *avlNode) int {
	return nodeHeight(n.left) - nodeHeight(n.right)
}

func rotateLeft(n *avlNode) *avlNode {
	r := n.right
	n.right = r.left
	r.left = n
	updateHeight(n)
	updateHeight(r)
	return r
}

func rotateRight(n *avlNode) *avlNode {
	l := n.left
	n.left = l.right
	l.right = n
	updateHeight(n)
	updateHeight(l)
	return l
}

func (t *avlTree) rebalance(n *avlNode) *avlNode {
	updateHeight(n)
	switch bf := balanceFactor(n); {
	case bf > 1:
		if balanceFactor(n.left) < 0 {
			n.left = rotateLeft(n.left)
		}
		return rotateRight(n)
	case bf < -1:
		if balanceFactor(n.right) > 0 {
			n.right = rotateRight(n.right)
		}
		return rotateLeft(n)
	default:
		return n
	}
}

// get returns the value stored under key.
func (t *avlTree) get(key []byte) ([]byte, bool) {
	n := t.root
	for n != nil {
		c := t.compare(key, n.key)
		switch {
		case c == 0:
			return n.value, true
		case c < 0:
			n = n.left
		default:
			n = n.right
		}
	}
	return nil, false
}

// insert stores key/value, returning true if the key is new.
func (t *avlTree) insert(key, value []byte) bool {
	var inserted bool
	t.root, inserted = t.insertRec(t.root, key, value)
	if inserted {
		t.size++
	}
	return inserted
}

func (t *avlTree) insertRec(n *avlNode, key, value []byte) (*avlNode, bool) {
	if n == nil {
		return &avlNode{key: key, value: value, height: 1}, true
	}
	c := t.compare(key, n.key)
	var inserted bool
	switch {
	case c == 0:
		n.value = value
		return n, false
	case c < 0:
		n.left, inserted = t.insertRec(n.left, key, value)
	default:
		n.right, inserted = t.insertRec(n.right, key, value)
	}
	return t.rebalance(n), inserted
}

// delete removes key, returning true if it was present.
func (t *avlTree) delete(key []byte) bool {
	var deleted bool
	t.root, deleted = t.deleteRec(t.root, key)
	if deleted {
		t.size--
	}
	return deleted
}

func (t *avlTree) deleteRec(n *avlNode, key []byte) (*avlNode, bool) {
	if n == nil {
		return nil, false
	}
	c := t.compare(key, n.key)
	var deleted bool
	switch {
	case c < 0:
		n.left, deleted = t.deleteRec(n.left, key)
	case c > 0:
		n.right, deleted = t.deleteRec(n.right, key)
	default:
		deleted = true
		if n.left == nil {
			return n.right, true
		}
		if n.right == nil {
			return n.left, true
		}
		succ := n.right
		for succ.left != nil {
			succ = succ.left
		}
		n.key, n.value = succ.key, succ.value
		n.right, _ = t.deleteRec(n.right, succ.key)
	}
	if !deleted {
		return n, false
	}
	return t.rebalance(n), true
}

// forEach visits entries in ascending raw-key order.
func (t *avlTree) forEach(fn func(key, value []byte)) {
	var walk func(n *avlNode)
	walk = func(n *avlNode) {
		if n == nil {
			return
		}
		walk(n.left)
		fn(n.key, n.value)
		walk(n.right)
	}
	walk(t.root)
}
