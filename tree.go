// Package snapiter provides a budget-limited snapshot iterator over a
// versioned tree (or DAG). When a node or byte budget is exhausted the
// iterator hands out a resume token bound to the tree version and the
// traversal stack; resuming from the token continues the traversal without
// re-emitting nodes. Any mutation of the tree bumps its version and
// invalidates previously issued tokens.
package snapiter

import (
	"crypto/rand"
	"encoding/hex"
)

// Node is a vertex of the tree. A Node may be shared: the same pointer may
// appear in the Children of several parents (a DAG). Shared nodes are
// emitted once per path that reaches them. Graphs must be acyclic.
type Node struct {
	ID       string
	Data     []byte
	Children []*Node
}

// size is the number of bytes the node charges against a byte budget.
func (n *Node) size() int {
	return len(n.ID) + len(n.Data)
}

// Tree is a versioned root handle. Every mutation increments the version,
// which invalidates all resume tokens issued before the mutation.
type Tree struct {
	id      string
	root    *Node
	version uint64
}

// NewTree returns a Tree wrapping root (root may be nil).
func NewTree(root *Node) *Tree {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return &Tree{id: hex.EncodeToString(buf[:]), root: root}
}

// Version returns the current tree version.
func (t *Tree) Version() uint64 { return t.version }

// Root returns the current root node.
func (t *Tree) Root() *Node { return t.root }

// ReplaceRoot swaps the root and bumps the version.
func (t *Tree) ReplaceRoot(root *Node) {
	t.root = root
	t.version++
}

// AddChild appends child to parent and bumps the version.
func (t *Tree) AddChild(parent, child *Node) {
	parent.Children = append(parent.Children, child)
	t.version++
}

// RemoveChildAt removes the i-th child of parent and bumps the version.
func (t *Tree) RemoveChildAt(parent *Node, i int) {
	parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
	t.version++
}

// SetData replaces the payload of n and bumps the version.
func (t *Tree) SetData(n *Node, data []byte) {
	n.Data = data
	t.version++
}
