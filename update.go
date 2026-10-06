package objtree

// Set returns a new tree in which the node at path is replaced by a leaf
// holding value. Missing intermediate nodes are created. Only the
// ancestors of path are copied; every other subtree is shared with the
// input tree and keeps its identity. The input tree is never modified.
//
// An empty path replaces the whole tree.
func Set(root *Node, path []string, value any) *Node {
	if len(path) == 0 {
		return &Node{Value: value}
	}
	var value0 any
	var children map[string]*Node
	if root != nil {
		value0 = root.Value
		children = cloneChildren(root.Children)
	} else {
		children = make(map[string]*Node, 1)
	}
	children[path[0]] = Set(child(root, path[0]), path[1:], value)
	return &Node{Value: value0, Children: children}
}

// Delete returns a new tree with the node at path removed. Only the
// ancestors of path are copied; untouched subtrees keep their identity.
// Deleting a path that does not exist returns the input tree unchanged
// (same pointer). The input tree is never modified.
//
// An empty path deletes the whole tree and returns nil.
func Delete(root *Node, path []string) *Node {
	if root == nil {
		return nil
	}
	if len(path) == 0 {
		return nil
	}
	c, ok := root.Children[path[0]]
	if !ok {
		return root
	}
	nc := Delete(c, path[1:])
	if nc == c {
		return root
	}
	children := cloneChildren(root.Children)
	if nc == nil {
		delete(children, path[0])
	} else {
		children[path[0]] = nc
	}
	return &Node{Value: root.Value, Children: children}
}

func child(n *Node, key string) *Node {
	if n == nil {
		return nil
	}
	return n.Children[key]
}
