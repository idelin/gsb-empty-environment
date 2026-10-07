package migrate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrCollision is returned when a rename would overwrite an existing
// sibling. The tree never overwrites: callers must resolve collisions
// before renaming.
var ErrCollision = errors.New("migrate: rename target already exists")

// ErrInjected is returned by Tree.Rename once the configured operation
// budget (MaxOps) is exhausted. Tests use it to simulate an interrupted
// migration.
var ErrInjected = errors.New("migrate: injected failure")

// Node is a single file or directory in a Tree. Renaming a directory
// implicitly moves all of its descendants, because paths are derived
// from the parent chain rather than stored.
type Node struct {
	ID       int
	Name     string
	Dir      bool
	Parent   *Node
	Children map[string]*Node
}

// Path returns the slash-separated absolute path of the node.
func (n *Node) Path() string {
	var parts []string
	for cur := n; cur != nil && cur.Parent != nil; cur = cur.Parent {
		parts = append(parts, cur.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

// Tree is an in-memory virtual file system used to plan and verify
// migrations without touching a real disk.
type Tree struct {
	Root *Node

	nodes  map[int]*Node
	nextID int

	// MaxOps, when positive, is the number of renames allowed before
	// Rename fails with ErrInjected. Zero means unlimited.
	MaxOps int
}

// NewTree returns an empty tree with a root directory.
func NewTree() *Tree {
	root := &Node{ID: 0, Name: "", Dir: true, Children: map[string]*Node{}}
	return &Tree{Root: root, nodes: map[int]*Node{0: root}, nextID: 1}
}

// Add creates the node at path, creating intermediate directories as
// needed, and returns the leaf. Existing nodes along the path are
// reused. The leaf is created as a directory iff dir is true.
func (t *Tree) Add(path string, dir bool) *Node {
	cur := t.Root
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" {
			continue
		}
		next, ok := cur.Children[seg]
		if !ok {
			next = &Node{
				ID:       t.nextID,
				Name:     seg,
				Dir:      true,
				Parent:   cur,
				Children: map[string]*Node{},
			}
			t.nextID++
			t.nodes[next.ID] = next
			cur.Children[seg] = next
		}
		cur = next
	}
	cur.Dir = dir
	return cur
}

// Get returns the node with the given ID, or nil.
func (t *Tree) Get(id int) *Node { return t.nodes[id] }

// Find returns the node at path, or nil if it does not exist.
func (t *Tree) Find(path string) *Node {
	cur := t.Root
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" {
			continue
		}
		next, ok := cur.Children[seg]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// Rename changes the name of n within its parent. It refuses to
// overwrite an existing sibling, returning ErrCollision instead.
func (t *Tree) Rename(n *Node, newName string) error {
	if t.MaxOps > 0 {
		t.MaxOps--
		if t.MaxOps == 0 {
			return ErrInjected
		}
	}
	if n.Parent == nil {
		return errors.New("migrate: cannot rename root")
	}
	if _, exists := n.Parent.Children[newName]; exists && newName != n.Name {
		return fmt.Errorf("%v: %s -> %s", ErrCollision, n.Path(), newName)
	}
	delete(n.Parent.Children, n.Name)
	n.Name = newName
	n.Parent.Children[newName] = n
	return nil
}

// Paths returns the sorted paths of every node except the root.
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

// Nodes returns every node except the root, ordered by ID.
func (t *Tree) Nodes() []*Node {
	out := make([]*Node, 0, len(t.nodes)-1)
	for id := 1; id < t.nextID; id++ {
		if n, ok := t.nodes[id]; ok {
			out = append(out, n)
		}
	}
	return out
}
