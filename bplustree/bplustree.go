// Package bplustree implements a B+ tree with an offline integrity
// inspector. The tree stores int keys with int values; internal nodes
// use fence-key semantics: keys[i] == maximum key of children[i], so a
// routing key must never exceed the max key of its left subtree.
package bplustree

import "sort"

// Node is a B+ tree node. Fields are exported so that offline tools
// (inspectors, debuggers, fault injectors) can walk and mutate the tree.
type Node struct {
	IsLeaf   bool
	Keys     []int
	Vals     []int   // leaf only, parallel to Keys
	Children []*Node // internal only, len == len(Keys)+1
	Parent   *Node
	Next     *Node // leaf level right sibling
	Prev     *Node // leaf level left sibling
}

// Tree is a B+ tree. It is not safe for concurrent use; callers must
// serialize access (e.g. with a sync.Mutex).
type Tree struct {
	Root  *Node
	Order int // max children of an internal node
}

// New returns an empty tree with the given order (>= 3).
func New(order int) *Tree {
	if order < 3 {
		order = 3
	}
	return &Tree{
		Order: order,
		Root:  &Node{IsLeaf: true},
	}
}

func (t *Tree) maxLeafKeys() int { return t.Order - 1 }
func (t *Tree) minLeafKeys() int { return t.Order / 2 }
func (t *Tree) minChildren() int { return (t.Order + 1) / 2 }

// childIndex returns the index of the child that may contain key.
func childIndex(n *Node, key int) int {
	return sort.Search(len(n.Keys), func(i int) bool { return n.Keys[i] >= key })
}

func (t *Tree) findLeaf(key int) *Node {
	n := t.Root
	for !n.IsLeaf {
		n = n.Children[childIndex(n, key)]
	}
	return n
}

// Get returns the value stored under key.
func (t *Tree) Get(key int) (int, bool) {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.Keys, key)
	if i < len(leaf.Keys) && leaf.Keys[i] == key {
		return leaf.Vals[i], true
	}
	return 0, false
}

// Insert adds or replaces key.
func (t *Tree) Insert(key, val int) {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.Keys, key)
	if i < len(leaf.Keys) && leaf.Keys[i] == key {
		leaf.Vals[i] = val
		return
	}
	leaf.Keys = append(leaf.Keys, 0)
	leaf.Vals = append(leaf.Vals, 0)
	copy(leaf.Keys[i+1:], leaf.Keys[i:])
	copy(leaf.Vals[i+1:], leaf.Vals[i:])
	leaf.Keys[i], leaf.Vals[i] = key, val
	if len(leaf.Keys) > t.maxLeafKeys() {
		t.splitLeaf(leaf)
	}
}

func (t *Tree) splitLeaf(leaf *Node) {
	mid := len(leaf.Keys) / 2
	right := &Node{IsLeaf: true, Parent: leaf.Parent}
	right.Keys = append(right.Keys, leaf.Keys[mid:]...)
	right.Vals = append(right.Vals, leaf.Vals[mid:]...)
	leaf.Keys = leaf.Keys[:mid]
	leaf.Vals = leaf.Vals[:mid]
	right.Next = leaf.Next
	if right.Next != nil {
		right.Next.Prev = right
	}
	right.Prev = leaf
	leaf.Next = right
	t.insertIntoParent(leaf, leaf.Keys[len(leaf.Keys)-1], right)
}

func (t *Tree) insertIntoParent(left *Node, key int, right *Node) {
	if left == t.Root {
		root := &Node{Keys: []int{key}, Children: []*Node{left, right}}
		left.Parent, right.Parent = root, root
		t.Root = root
		return
	}
	parent := left.Parent
	idx := indexOfChild(parent, left)
	parent.Children = append(parent.Children, nil)
	copy(parent.Children[idx+2:], parent.Children[idx+1:])
	parent.Children[idx+1] = right
	right.Parent = parent
	parent.Keys = append(parent.Keys, 0)
	copy(parent.Keys[idx+1:], parent.Keys[idx:])
	parent.Keys[idx] = key
	if len(parent.Children) > t.Order {
		t.splitInternal(parent)
	}
}

func (t *Tree) splitInternal(n *Node) {
	mid := len(n.Children) / 2
	right := &Node{Parent: n.Parent}
	right.Children = append(right.Children, n.Children[mid:]...)
	for _, c := range right.Children {
		c.Parent = right
	}
	up := n.Keys[mid-1] // == subtreeMax(left half)
	n.Children = n.Children[:mid]
	recomputeKeys(n)
	recomputeKeys(right)
	t.insertIntoParent(n, up, right)
}

// Delete removes key, rebalancing via borrow/merge. Returns false if
// the key was absent.
func (t *Tree) Delete(key int) bool {
	leaf := t.findLeaf(key)
	i := sort.SearchInts(leaf.Keys, key)
	if i >= len(leaf.Keys) || leaf.Keys[i] != key {
		return false
	}
	leaf.Keys = append(leaf.Keys[:i], leaf.Keys[i+1:]...)
	leaf.Vals = append(leaf.Vals[:i], leaf.Vals[i+1:]...)
	if leaf == t.Root {
		return true
	}
	if len(leaf.Keys) >= t.minLeafKeys() {
		refreshSeparators(leaf.Parent)
		return true
	}
	t.rebalanceLeaf(leaf)
	return true
}

func (t *Tree) rebalanceLeaf(leaf *Node) {
	parent := leaf.Parent
	idx := indexOfChild(parent, leaf)
	if idx > 0 {
		if left := parent.Children[idx-1]; len(left.Keys) > t.minLeafKeys() {
			leaf.Keys = append([]int{left.Keys[len(left.Keys)-1]}, leaf.Keys...)
			leaf.Vals = append([]int{left.Vals[len(left.Vals)-1]}, leaf.Vals...)
			left.Keys = left.Keys[:len(left.Keys)-1]
			left.Vals = left.Vals[:len(left.Vals)-1]
			refreshSeparators(parent)
			return
		}
	}
	if idx < len(parent.Children)-1 {
		if right := parent.Children[idx+1]; len(right.Keys) > t.minLeafKeys() {
			leaf.Keys = append(leaf.Keys, right.Keys[0])
			leaf.Vals = append(leaf.Vals, right.Vals[0])
			right.Keys = right.Keys[1:]
			right.Vals = right.Vals[1:]
			refreshSeparators(parent)
			return
		}
	}
	if idx > 0 {
		left := parent.Children[idx-1]
		left.Keys = append(left.Keys, leaf.Keys...)
		left.Vals = append(left.Vals, leaf.Vals...)
		left.Next = leaf.Next
		if leaf.Next != nil {
			leaf.Next.Prev = left
		}
		removeChild(parent, idx)
	} else {
		right := parent.Children[1]
		leaf.Keys = append(leaf.Keys, right.Keys...)
		leaf.Vals = append(leaf.Vals, right.Vals...)
		leaf.Next = right.Next
		if right.Next != nil {
			right.Next.Prev = leaf
		}
		removeChild(parent, 1)
	}
	t.fixInternal(parent)
}

func (t *Tree) fixInternal(n *Node) {
	if n == t.Root {
		if !n.IsLeaf && len(n.Children) == 1 {
			t.Root = n.Children[0]
			t.Root.Parent = nil
		}
		return
	}
	if len(n.Children) >= t.minChildren() {
		refreshSeparators(n)
		return
	}
	parent := n.Parent
	idx := indexOfChild(parent, n)
	if idx > 0 {
		if left := parent.Children[idx-1]; len(left.Children) > t.minChildren() {
			n.Children = append([]*Node{left.Children[len(left.Children)-1]}, n.Children...)
			n.Children[0].Parent = n
			left.Children = left.Children[:len(left.Children)-1]
			recomputeKeys(left)
			recomputeKeys(n)
			refreshSeparators(parent)
			return
		}
	}
	if idx < len(parent.Children)-1 {
		if right := parent.Children[idx+1]; len(right.Children) > t.minChildren() {
			n.Children = append(n.Children, right.Children[0])
			n.Children[len(n.Children)-1].Parent = n
			right.Children = right.Children[1:]
			recomputeKeys(right)
			recomputeKeys(n)
			refreshSeparators(parent)
			return
		}
	}
	if idx > 0 {
		left := parent.Children[idx-1]
		left.Children = append(left.Children, n.Children...)
		for _, c := range n.Children {
			c.Parent = left
		}
		recomputeKeys(left)
		removeChild(parent, idx)
	} else {
		right := parent.Children[1]
		n.Children = append(n.Children, right.Children...)
		for _, c := range right.Children {
			c.Parent = n
		}
		recomputeKeys(n)
		removeChild(parent, 1)
	}
	t.fixInternal(parent)
}

func indexOfChild(parent, child *Node) int {
	for i, c := range parent.Children {
		if c == child {
			return i
		}
	}
	return -1
}

func removeChild(parent *Node, idx int) {
	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)
	recomputeKeys(parent)
}

// recomputeKeys rebuilds n.Keys from the maximum keys of its subtrees.
func recomputeKeys(n *Node) {
	if n.IsLeaf {
		return
	}
	keys := make([]int, len(n.Children)-1)
	for i := 0; i < len(n.Children)-1; i++ {
		keys[i] = subtreeMax(n.Children[i])
	}
	n.Keys = keys
}

// refreshSeparators recomputes separator keys of n and all ancestors.
func refreshSeparators(n *Node) {
	for ; n != nil; n = n.Parent {
		recomputeKeys(n)
	}
}

func subtreeMax(n *Node) int {
	for !n.IsLeaf {
		n = n.Children[len(n.Children)-1]
	}
	return n.Keys[len(n.Keys)-1]
}

// Size counts keys by walking the leaf chain.
func (t *Tree) Size() int {
	n := 0
	for leaf := t.leftmost(); leaf != nil; leaf = leaf.Next {
		n += len(leaf.Keys)
	}
	return n
}

func (t *Tree) leftmost() *Node {
	n := t.Root
	for n != nil && !n.IsLeaf {
		n = n.Children[0]
	}
	return n
}
