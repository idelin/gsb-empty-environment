package migrate

import (
	"fmt"
	"sort"
	"strings"
)

// Node is a file or directory in the virtual file tree.
type Node struct {
	Name     string
	Dir      bool
	Content  []byte
	Parent   *Node
	Children map[string]*Node
}

// Tree is an in-memory virtual file tree rooted at an unnamed root.
type Tree struct {
	Root *Node
}

func NewTree() *Tree {
	return &Tree{Root: &Node{Dir: true, Children: map[string]*Node{}}}
}

// Add creates the node at path (and any missing parent directories) and
// returns it. Paths may start with "/" or not. Existing nodes are returned
// as-is.
func (t *Tree) Add(path string, dir bool) *Node {
	cur := t.Root
	for _, comp := range splitPath(path) {
		next, ok := cur.Children[comp]
		if !ok {
			next = &Node{Name: comp, Dir: true, Parent: cur, Children: map[string]*Node{}}
			cur.Children[comp] = next
		}
		cur = next
	}
	cur.Dir = dir
	if dir && cur.Children == nil {
		cur.Children = map[string]*Node{}
	}
	return cur
}

// Find returns the node at path, or nil.
func (t *Tree) Find(path string) *Node {
	cur := t.Root
	for _, comp := range splitPath(path) {
		next, ok := cur.Children[comp]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// Path returns the absolute path of the node. The root has path "".
func (n *Node) Path() string {
	if n.Parent == nil {
		return ""
	}
	return n.Parent.Path() + "/" + n.Name
}

// Rename changes the node's base name within its parent. It fails if a
// sibling with the new name already exists: renames never overwrite.
func (n *Node) Rename(newName string) error {
	if n.Parent == nil {
		return fmt.Errorf("migrate: cannot rename root")
	}
	if newName == n.Name {
		return nil
	}
	if _, exists := n.Parent.Children[newName]; exists {
		return fmt.Errorf("migrate: %q already exists in %q", newName, n.Parent.Path())
	}
	delete(n.Parent.Children, n.Name)
	n.Name = newName
	n.Parent.Children[newName] = n
	return nil
}

// Paths returns the sorted absolute paths of all nodes except the root.
func (t *Tree) Paths() []string {
	var out []string
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			out = append(out, c.Path())
			walk(c)
		}
	}
	walk(t.Root)
	sort.Strings(out)
	return out
}

func splitPath(path string) []string {
	var comps []string
	for _, c := range strings.Split(path, "/") {
		if c != "" {
			comps = append(comps, c)
		}
	}
	return comps
}

func sortedKeys(m map[string]*Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
