// Package bptree implements a B+ tree with an offline integrity inspector.
//
// Tree convention: in an internal node with children c[0..m] and keys
// k[0..m-1], the routing key k[i] equals the maximum key of the subtree
// rooted at c[i]. The last child c[m] has no routing key (its range is
// (k[m-1], +inf)). Search descends to the first child i with key <= k[i].
package bptree

import (
	"sort"
	"sync"
)

const (
	// MaxKeys is the maximum number of keys a node may hold before split.
	MaxKeys = 4
	// MinKeys is the minimum number of keys a non-root node must hold.
	MinKeys = 2
)

// Node is a B+ tree node. Internal nodes use keys+children; leaves use
// keys+vals and form a doubly linked list via prev/next.
type Node struct {
	leaf     bool
	keys     []int
	children []*Node // internal nodes only: len(children) == len(keys)+1
	vals     []int   // leaves only: len(vals) == len(keys)
	next     *Node   // leaves only
	prev     *Node   // leaves only
	parent   *Node
}

// Tree is a B+ tree safe for concurrent use.
type Tree struct {
	mu   sync.RWMutex
	root *Node
}

// New returns an empty tree.
func New() *Tree {
	return &Tree{root: &Node{leaf: true}}
}

// Root returns the root node. Callers must not mutate it; use Inspect.
func (t *Tree) Root() *Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root
}

// IsLeaf reports whether n is a leaf.
func (n *Node) IsLeaf() bool { return n.leaf }

// Keys returns a copy of the node's keys.
func (n *Node) Keys() []int { return append([]int(nil), n.keys...) }

// Children returns a copy of the node's children slice.
func (n *Node) Children() []*Node { return append([]*Node(nil), n.children...) }

// Next returns the next leaf in the chain (nil for internal nodes).
func (n *Node) Next() *Node { return n.next }

// Prev returns the previous leaf in the chain (nil for internal nodes).
func (n *Node) Prev() *Node { return n.prev }

// Parent returns the node's parent.
func (n *Node) Parent() *Node { return n.parent }

// Get returns the value stored under key.
func (t *Tree) Get(key int) (int, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.root
	for !n.leaf {
		i := sort.Search(len(n.keys), func(i int) bool { return key <= n.keys[i] })
		n = n.children[i]
	}
	i := sort.SearchInts(n.keys, key)
	if i < len(n.keys) && n.keys[i] == key {
		return n.vals[i], true
	}
	return 0, false
}

// Insert upserts key with value val.
func (t *Tree) Insert(key, val int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	promote, right, split := t.insertRec(t.root, key, val)
	if split {
		nr := &Node{keys: []int{promote}, children: []*Node{t.root, right}}
		t.root.parent = nr
		right.parent = nr
		t.root = nr
	}
}

// insertRec inserts into the subtree rooted at n. On split it returns the
// routing key to promote (the max key of the left half) and the new right
// sibling.
func (t *Tree) insertRec(n *Node, key, val int) (int, *Node, bool) {
	if n.leaf {
		i := sort.SearchInts(n.keys, key)
		if i < len(n.keys) && n.keys[i] == key {
			n.vals[i] = val
			return 0, nil, false
		}
		n.keys = insertInt(n.keys, i, key)
		n.vals = insertInt(n.vals, i, val)
		if len(n.keys) <= MaxKeys {
			return 0, nil, false
		}
		mid := len(n.keys) / 2
		right := &Node{leaf: true, parent: n.parent}
		right.keys = append(right.keys, n.keys[mid:]...)
		right.vals = append(right.vals, n.vals[mid:]...)
		n.keys = n.keys[:mid]
		n.vals = n.vals[:mid]
		right.next = n.next
		if n.next != nil {
			n.next.prev = right
		}
		n.next = right
		right.prev = n
		return n.keys[mid-1], right, true
	}

	i := sort.Search(len(n.keys), func(i int) bool { return key <= n.keys[i] })
	promote, right, split := t.insertRec(n.children[i], key, val)
	if !split {
		// The subtree max may have grown; refresh the routing key.
		if i < len(n.keys) && key > n.keys[i] {
			n.keys[i] = key
		}
		return 0, nil, false
	}
	n.keys = insertInt(n.keys, i, promote)
	n.children = insertNode(n.children, i+1, right)
	right.parent = n
	if len(n.keys) <= MaxKeys {
		return 0, nil, false
	}
	mid := len(n.keys) / 2
	up := n.keys[mid]
	rnode := &Node{parent: n.parent}
	rnode.keys = append(rnode.keys, n.keys[mid+1:]...)
	rnode.children = append(rnode.children, n.children[mid+1:]...)
	for _, c := range rnode.children {
		c.parent = rnode
	}
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	return up, rnode, true
}

// Delete removes key, returning whether it was present.
func (t *Tree) Delete(key int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.deleteRec(t.root, key) {
		return false
	}
	for !t.root.leaf && len(t.root.children) == 1 {
		t.root = t.root.children[0]
		t.root.parent = nil
	}
	return true
}

func (t *Tree) deleteRec(n *Node, key int) bool {
	if n.leaf {
		i := sort.SearchInts(n.keys, key)
		if i >= len(n.keys) || n.keys[i] != key {
			return false
		}
		n.keys = removeInt(n.keys, i)
		n.vals = removeInt(n.vals, i)
		return true
	}
	i := sort.Search(len(n.keys), func(i int) bool { return key <= n.keys[i] })
	if !t.deleteRec(n.children[i], key) {
		return false
	}
	child := n.children[i]
	if i < len(n.keys) && len(child.keys) > 0 {
		// The subtree max may have shrunk; refresh the routing key.
		n.keys[i] = subtreeMax(child)
	}
	if len(child.keys) < MinKeys {
		t.rebalance(n, i)
		for j := range n.keys {
			n.keys[j] = subtreeMax(n.children[j])
		}
	}
	return true
}

// rebalance fixes an underflowed child p.children[i] by borrowing from a
// sibling or merging. Parent routing keys are refreshed by the caller.
func (t *Tree) rebalance(p *Node, i int) {
	c := p.children[i]
	// Borrow from the left sibling.
	if i > 0 && len(p.children[i-1].keys) > MinKeys {
		l := p.children[i-1]
		if c.leaf {
			c.keys = insertInt(c.keys, 0, l.keys[len(l.keys)-1])
			c.vals = insertInt(c.vals, 0, l.vals[len(l.vals)-1])
			l.keys = l.keys[:len(l.keys)-1]
			l.vals = l.vals[:len(l.vals)-1]
		} else {
			c.children = insertNode(c.children, 0, l.children[len(l.children)-1])
			c.children[0].parent = c
			l.children = l.children[:len(l.children)-1]
			c.keys = insertInt(c.keys, 0, p.keys[i-1])
			l.keys = l.keys[:len(l.keys)-1]
		}
		return
	}
	// Borrow from the right sibling.
	if i < len(p.children)-1 && len(p.children[i+1].keys) > MinKeys {
		r := p.children[i+1]
		if c.leaf {
			c.keys = append(c.keys, r.keys[0])
			c.vals = append(c.vals, r.vals[0])
			r.keys = r.keys[1:]
			r.vals = r.vals[1:]
		} else {
			c.children = append(c.children, r.children[0])
			c.children[len(c.children)-1].parent = c
			r.children = r.children[1:]
			c.keys = append(c.keys, p.keys[i])
			r.keys = r.keys[1:]
		}
		return
	}
	// Merge.
	if i > 0 {
		l := p.children[i-1]
		if c.leaf {
			l.keys = append(l.keys, c.keys...)
			l.vals = append(l.vals, c.vals...)
			l.next = c.next
			if c.next != nil {
				c.next.prev = l
			}
		} else {
			l.keys = append(l.keys, p.keys[i-1])
			l.keys = append(l.keys, c.keys...)
			for _, ch := range c.children {
				ch.parent = l
			}
			l.children = append(l.children, c.children...)
		}
		p.children = removeNode(p.children, i)
		p.keys = removeInt(p.keys, i-1)
	} else {
		r := p.children[1]
		if c.leaf {
			c.keys = append(c.keys, r.keys...)
			c.vals = append(c.vals, r.vals...)
			c.next = r.next
			if r.next != nil {
				r.next.prev = c
			}
		} else {
			c.keys = append(c.keys, p.keys[0])
			c.keys = append(c.keys, r.keys...)
			for _, ch := range r.children {
				ch.parent = c
			}
			c.children = append(c.children, r.children...)
		}
		p.children = removeNode(p.children, 1)
		p.keys = removeInt(p.keys, 0)
	}
}

// subtreeMax returns the maximum key stored in the subtree rooted at n.
// It must not be called on an empty leaf.
func subtreeMax(n *Node) int {
	for !n.leaf {
		n = n.children[len(n.children)-1]
	}
	return n.keys[len(n.keys)-1]
}

// Scan returns all keys in ascending order by walking the leaf chain.
func (t *Tree) Scan() []int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.root
	for !n.leaf {
		n = n.children[0]
	}
	var out []int
	for n != nil {
		out = append(out, n.keys...)
		n = n.next
	}
	return out
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

func insertNode(s []*Node, i int, n *Node) []*Node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = n
	return s
}

func removeNode(s []*Node, i int) []*Node {
	return append(s[:i], s[i+1:]...)
}
