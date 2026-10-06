// Package objtree implements a persistent (immutable) object tree.
//
// Every update returns a new root and copies only the ancestors of the
// modified path; all untouched subtrees are shared with the previous
// version and keep their object identity. Old roots are never mutated,
// so they serve as stable snapshots.
package objtree

import "reflect"

// Node is a node of the object tree. A node with a nil Children map is a
// leaf and only its Value is meaningful. A node may carry both a Value and
// children; turning a leaf into an internal node preserves its Value.
type Node struct {
	Value    any
	Children map[string]*Node
}

// Leaf returns a leaf node holding v.
func Leaf(v any) *Node { return &Node{Value: v} }

// Internal returns an internal node with the given children.
func Internal(children map[string]*Node) *Node { return &Node{Children: children} }

// Get returns the node at path, or nil if the path does not exist.
func Get(root *Node, path []string) *Node {
	cur := root
	for _, seg := range path {
		if cur == nil {
			return nil
		}
		cur = cur.Children[seg]
	}
	return cur
}

// Clone returns a deep copy of the subtree rooted at root.
func Clone(root *Node) *Node {
	if root == nil {
		return nil
	}
	cp := &Node{Value: root.Value}
	if root.Children != nil {
		cp.Children = make(map[string]*Node, len(root.Children))
		for k, v := range root.Children {
			cp.Children[k] = Clone(v)
		}
	}
	return cp
}

// Equal reports whether a and b are structurally equal (same shape and
// deeply equal values). Pointer identity is irrelevant.
func Equal(a, b *Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !reflect.DeepEqual(a.Value, b.Value) {
		return false
	}
	if len(a.Children) != len(b.Children) {
		return false
	}
	for k, ac := range a.Children {
		bc, ok := b.Children[k]
		if !ok || !Equal(ac, bc) {
			return false
		}
	}
	return true
}

// cloneChildren returns a shallow copy of a children map.
func cloneChildren(children map[string]*Node) map[string]*Node {
	cp := make(map[string]*Node, len(children)+1)
	for k, v := range children {
		cp[k] = v
	}
	return cp
}
