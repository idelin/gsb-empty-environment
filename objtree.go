// Package objtree implements persistent (immutable) updates over an object
// tree. Updating a path copies only the ancestors of that path; every
// untouched subtree keeps its identity and is shared between the old and
// the new root.
package objtree

import "fmt"

// Node is an object-tree node. A node with a nil Children map is a leaf and
// holds Value; otherwise it is a branch and Value is ignored.
type Node struct {
	Value    interface{}
	Children map[string]*Node
}

// Leaf returns a leaf node holding v.
func Leaf(v interface{}) *Node { return &Node{Value: v} }

// Branch returns a branch node with the given children.
func Branch(children map[string]*Node) *Node { return &Node{Children: children} }

// IsLeaf reports whether n is a leaf.
func (n *Node) IsLeaf() bool { return n == nil || n.Children == nil }

// Get returns the node at path, or nil if it does not exist.
func Get(root *Node, path []string) *Node {
	cur := root
	for _, seg := range path {
		if cur == nil || cur.Children == nil {
			return nil
		}
		cur = cur.Children[seg]
	}
	return cur
}

// Set returns a new tree with the node at path replaced by child. Only the
// ancestors of path are copied; all other subtrees are shared with the
// original tree. Intermediate leaves are replaced by branches as needed.
func Set(root *Node, path []string, child *Node) *Node {
	if len(path) == 0 {
		return child
	}
	if root == nil {
		root = &Node{}
	}
	children := make(map[string]*Node, len(root.Children)+1)
	for k, v := range root.Children {
		children[k] = v
	}
	children[path[0]] = Set(root.Children[path[0]], path[1:], child)
	return &Node{Children: children}
}

// Delete returns a new tree with the node at path removed. If path does not
// exist the original root is returned unchanged (same pointer).
func Delete(root *Node, path []string) *Node {
	if root == nil {
		return nil
	}
	if len(path) == 0 {
		return nil
	}
	old, ok := root.Children[path[0]]
	if !ok {
		return root
	}
	if len(path) == 1 {
		children := make(map[string]*Node, len(root.Children))
		for k, v := range root.Children {
			if k != path[0] {
				children[k] = v
			}
		}
		return &Node{Children: children}
	}
	next := Delete(old, path[1:])
	if next == old {
		return root
	}
	children := make(map[string]*Node, len(root.Children))
	for k, v := range root.Children {
		children[k] = v
	}
	if next == nil {
		delete(children, path[0])
	} else {
		children[path[0]] = next
	}
	return &Node{Children: children}
}

// Op is a single transactional operation. Set=true replaces the node at
// Path with Value; Set=false deletes the node at Path.
type Op struct {
	Path  []string
	Set   bool
	Value *Node
}

// ApplyTransaction applies ops atomically and returns the new root.
//
// Ops are grouped by common ancestors and applied in a single recursive
// pass, so sibling updates (e.g. /a/b and /a/c) merge into one copy of /a
// instead of overwriting each other. A delete whose path is an ancestor of
// another op's path is a conflict and the whole transaction is rejected.
// An empty transaction returns root unchanged (same pointer).
func ApplyTransaction(root *Node, ops []Op) (*Node, error) {
	if err := checkConflicts(ops); err != nil {
		return nil, err
	}
	return applyOps(root, ops), nil
}

func checkConflicts(ops []Op) error {
	for i, a := range ops {
		if a.Set {
			continue
		}
		for j, b := range ops {
			if i == j {
				continue
			}
			if isStrictPrefix(a.Path, b.Path) {
				return fmt.Errorf("objtree: delete of %v conflicts with operation on %v", a.Path, b.Path)
			}
		}
	}
	return nil
}

func isStrictPrefix(prefix, path []string) bool {
	if len(prefix) >= len(path) {
		return false
	}
	for i, seg := range prefix {
		if path[i] != seg {
			return false
		}
	}
	return true
}

// applyOps applies ops (all relative to node) and returns the updated node.
// It returns node itself when no op affects it, preserving identity.
func applyOps(node *Node, ops []Op) *Node {
	base := node
	var sub []Op
	for _, op := range ops {
		if len(op.Path) == 0 {
			if op.Set {
				base = op.Value // replace, then apply deeper ops on top
			} else {
				base = nil // delete; deeper ops were rejected as conflicts
			}
		} else {
			sub = append(sub, op)
		}
	}
	if len(sub) == 0 {
		return base
	}
	groups := make(map[string][]Op)
	order := []string{}
	for _, op := range sub {
		head := op.Path[0]
		if _, ok := groups[head]; !ok {
			order = append(order, head)
		}
		groups[head] = append(groups[head], Op{Path: op.Path[1:], Set: op.Set, Value: op.Value})
	}
	var baseChildren map[string]*Node
	if base != nil {
		baseChildren = base.Children
	}
	var children map[string]*Node
	copied := false
	for _, head := range order {
		old := baseChildren[head]
		next := applyOps(old, groups[head])
		if next == old {
			continue
		}
		if !copied {
			children = make(map[string]*Node, len(baseChildren))
			for k, v := range baseChildren {
				children[k] = v
			}
			copied = true
		}
		if next == nil {
			delete(children, head)
		} else {
			children[head] = next
		}
	}
	if !copied {
		if base == nil || base.Children == nil {
			// base is absent or a leaf: a descending set turns it into a
			// branch even when the child operations net out to no change
			// (e.g. set followed by delete of the same key).
			for _, op := range sub {
				if op.Set {
					return &Node{Children: map[string]*Node{}}
				}
			}
		}
		return base
	}
	return &Node{Children: children}
}
