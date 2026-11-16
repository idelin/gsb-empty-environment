package netsec

import (
	"fmt"
	"sort"
)

// TreeNode describes a wallet tree node and its direct children. Paths are
// absolute; a child path must add exactly one fragment to its parent.
type TreeNode struct {
	Path     Path        `json:"path"`
	Children []*TreeNode `json:"children,omitempty"`
}

type treeNode struct {
	path     Path
	parent   *treeNode
	children map[Fragment]*treeNode
}

// Tree is the wallet's node set. Parent-child links are implied by derivation
// paths, so callers may supply only the nodes they care about; missing
// ancestors are added automatically.
type Tree struct {
	root  *treeNode
	nodes map[string]*treeNode
}

// NewTree builds a wallet tree from absolute node paths.
func NewTree(paths []Path) (*Tree, error) {
	tree := &Tree{nodes: make(map[string]*treeNode)}
	root := &treeNode{path: Path{}, children: make(map[Fragment]*treeNode)}
	tree.root = root
	tree.nodes[root.path.String()] = root

	for _, path := range paths {
		if err := tree.Add(path); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// NewTreeFromStrings is NewTree with string paths.
func NewTreeFromStrings(paths []string) (*Tree, error) {
	parsed := make([]Path, 0, len(paths))
	for _, text := range paths {
		path, err := ParsePath(text)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, path)
	}
	return NewTree(parsed)
}

// NewTreeFromStructure builds a tree from a nested node description. Every
// child must be an immediate non-duplicate child of its enclosing node.
func NewTreeFromStructure(root *TreeNode) (*Tree, error) {
	if root == nil {
		return nil, fmt.Errorf("nil wallet tree root")
	}
	if len(root.Path) != 0 {
		return nil, fmt.Errorf("wallet tree root must be the master path m, got %s", root.Path)
	}

	tree, err := NewTree(nil)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{root.Path.String(): true}
	var add func(parent Path, node *TreeNode) error
	add = func(parent Path, node *TreeNode) error {
		if node == nil {
			return fmt.Errorf("nil wallet tree node below %s", parent)
		}
		if !node.Path.HasPrefix(parent) || len(node.Path) != len(parent)+1 {
			return fmt.Errorf("node %s is not an immediate child of %s", node.Path, parent)
		}
		key := node.Path.String()
		if seen[key] {
			return fmt.Errorf("duplicate wallet tree node %s", key)
		}
		seen[key] = true
		if err := tree.Add(node.Path); err != nil {
			return err
		}
		for _, child := range node.Children {
			if err := add(node.Path, child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range root.Children {
		if err := add(root.Path, child); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// Add inserts a node and any missing ancestors.
func (t *Tree) Add(path Path) error {
	if t == nil {
		return fmt.Errorf("nil wallet tree")
	}
	if path == nil {
		return nil
	}

	current := t.root
	for i, fragment := range path {
		next := current.children[fragment]
		if next == nil {
			nextPath := path[:i+1].Clone()
			next = &treeNode{
				path:     nextPath,
				parent:   current,
				children: make(map[Fragment]*treeNode),
			}
			current.children[fragment] = next
			t.nodes[nextPath.String()] = next
		}
		current = next
	}
	return nil
}

// Contains reports whether the tree contains the node.
func (t *Tree) Contains(path Path) bool {
	if t == nil {
		return false
	}
	_, ok := t.nodes[path.String()]
	return ok
}

// Nodes returns every node in deterministic path order.
func (t *Tree) Nodes() []Path {
	if t == nil {
		return nil
	}
	paths := make([]Path, 0, len(t.nodes))
	for _, node := range t.nodes {
		paths = append(paths, node.path.Clone())
	}
	sort.Slice(paths, func(i, j int) bool {
		return paths[i].Compare(paths[j]) < 0
	})
	return paths
}

// Children returns the direct children of path in deterministic order.
func (t *Tree) Children(path Path) []Path {
	if t == nil {
		return nil
	}
	node := t.nodes[path.String()]
	if node == nil {
		return nil
	}
	children := make([]*treeNode, 0, len(node.children))
	for _, child := range node.children {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].path.Compare(children[j].path) < 0
	})
	result := make([]Path, len(children))
	for i, child := range children {
		result[i] = child.path.Clone()
	}
	return result
}

func (t *Tree) sortedNodes() []*treeNode {
	nodes := make([]*treeNode, 0, len(t.nodes))
	for _, node := range t.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].path.Compare(nodes[j].path) < 0
	})
	return nodes
}

func (t *Tree) sortedChildren(node *treeNode) []*treeNode {
	children := make([]*treeNode, 0, len(node.children))
	for _, child := range node.children {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].path.Compare(children[j].path) < 0
	})
	return children
}

func (t *Tree) descendants(node *treeNode, includeSelf bool) []*treeNode {
	result := make([]*treeNode, 0)
	if includeSelf {
		result = append(result, node)
	}
	var walk func(*treeNode)
	walk = func(current *treeNode) {
		for _, child := range t.sortedChildren(current) {
			result = append(result, child)
			walk(child)
		}
	}
	walk(node)
	return result
}
