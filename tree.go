package main

import (
	"fmt"
	"sort"
	"strings"
)

// NodeID is a stable identity assigned at creation and never reused.
// A path only denotes a node's current location, never its identity.
type NodeID uint64

// RootID is the identity of the root directory.
const RootID NodeID = 0

// Node is a file or directory in the synthetic tree.
type Node struct {
	ID       NodeID
	Name     string
	Dir      bool
	Parent   *Node
	Children map[string]*Node
}

// Tree is the primary tree model. All mutating operations validate
// everything up front and only then commit, so a failed operation
// never leaves a partial mutation behind (transactional atomicity).
type Tree struct {
	Root   *Node
	nextID NodeID
	Nodes  map[NodeID]*Node
}

// NewTree returns an empty tree holding only the root directory.
func NewTree() *Tree {
	root := &Node{ID: RootID, Name: "", Dir: true, Children: map[string]*Node{}}
	return &Tree{Root: root, nextID: 1, Nodes: map[NodeID]*Node{RootID: root}}
}

func splitPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("path %q is not absolute", p)
	}
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	for _, part := range parts {
		if part == "." || part == ".." {
			return nil, fmt.Errorf("path %q contains forbidden component %q", p, part)
		}
	}
	return parts, nil
}

func splitParent(p string) (dir, base string, err error) {
	parts, err := splitPath(p)
	if err != nil {
		return "", "", err
	}
	if len(parts) == 0 {
		return "", "", fmt.Errorf("path %q is the root and has no parent", p)
	}
	return "/" + strings.Join(parts[:len(parts)-1], "/"), parts[len(parts)-1], nil
}

func (t *Tree) resolve(p string) (*Node, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, err
	}
	cur := t.Root
	for _, part := range parts {
		if !cur.Dir {
			return nil, fmt.Errorf("path %q: %q is not a directory", p, cur.Name)
		}
		next, ok := cur.Children[part]
		if !ok {
			return nil, fmt.Errorf("path %q: component %q does not exist", p, part)
		}
		cur = next
	}
	return cur, nil
}

// PathOf reports the current location of n. Locations change on rename;
// identities (Node.ID) never do.
func PathOf(n *Node) string {
	if n.Parent == nil {
		return "/"
	}
	var parts []string
	for cur := n; cur.Parent != nil; cur = cur.Parent {
		parts = append(parts, cur.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

// Create allocates a fresh stable ID and links a new node at path.
func (t *Tree) Create(path string, dir bool) (*Node, error) {
	parentPath, name, err := splitParent(path)
	if err != nil {
		return nil, err
	}
	parent, err := t.resolve(parentPath)
	if err != nil {
		return nil, err
	}
	if !parent.Dir {
		return nil, fmt.Errorf("create %q: parent is not a directory", path)
	}
	if _, ok := parent.Children[name]; ok {
		return nil, fmt.Errorf("create %q: name already exists", path)
	}
	// commit point: nothing below can fail
	n := &Node{ID: t.nextID, Name: name, Dir: dir, Parent: parent}
	t.nextID++
	if dir {
		n.Children = map[string]*Node{}
	}
	parent.Children[name] = n
	t.Nodes[n.ID] = n
	return n, nil
}

// Unlink removes the node at path. Directories must be empty.
func (t *Tree) Unlink(path string) (*Node, error) {
	n, err := t.resolve(path)
	if err != nil {
		return nil, err
	}
	if n == t.Root {
		return nil, fmt.Errorf("unlink %q: cannot unlink the root", path)
	}
	if n.Dir && len(n.Children) > 0 {
		return nil, fmt.Errorf("unlink %q: directory not empty", path)
	}
	// commit point
	delete(n.Parent.Children, n.Name)
	delete(t.Nodes, n.ID)
	n.Parent = nil
	return n, nil
}

// Rename moves src to dst, replacing an existing compatible target.
// A directory can never be moved into its own subtree (no parent cycles).
// All validation happens before the commit point, so a failed rename
// leaves both names intact.
func (t *Tree) Rename(src, dst string) (moved, replaced *Node, err error) {
	s, err := t.resolve(src)
	if err != nil {
		return nil, nil, err
	}
	if s == t.Root {
		return nil, nil, fmt.Errorf("rename %q: cannot rename the root", src)
	}
	dstParentPath, dstName, err := splitParent(dst)
	if err != nil {
		return nil, nil, err
	}
	dp, err := t.resolve(dstParentPath)
	if err != nil {
		return nil, nil, err
	}
	if !dp.Dir {
		return nil, nil, fmt.Errorf("rename %q -> %q: destination parent is not a directory", src, dst)
	}
	if s.Dir {
		for cur := dp; cur != nil; cur = cur.Parent {
			if cur == s {
				return nil, nil, fmt.Errorf("rename %q -> %q: would create a parent cycle", src, dst)
			}
		}
	}
	existing, occupied := dp.Children[dstName]
	if occupied && existing != s {
		if existing.Dir != s.Dir {
			return nil, nil, fmt.Errorf("rename %q -> %q: cannot replace %s with %s",
				src, dst, kindOf(existing.Dir), kindOf(s.Dir))
		}
		if existing.Dir && len(existing.Children) > 0 {
			return nil, nil, fmt.Errorf("rename %q -> %q: target directory not empty", src, dst)
		}
	}
	// commit point: nothing below can fail
	if occupied {
		if existing == s {
			return s, nil, nil // rename onto itself: no-op
		}
		delete(dp.Children, dstName)
		delete(t.Nodes, existing.ID)
		existing.Parent = nil
		replaced = existing
	}
	delete(s.Parent.Children, s.Name)
	s.Parent = dp
	s.Name = dstName
	dp.Children[dstName] = s
	return s, replaced, nil
}

func kindOf(dir bool) string {
	if dir {
		return "directory"
	}
	return "file"
}

// Mapping returns the full path -> identity snapshot of the live tree.
func (t *Tree) Mapping() map[string]NodeID {
	m := map[string]NodeID{}
	var walk func(n *Node)
	walk = func(n *Node) {
		m[PathOf(n)] = n.ID
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t.Root)
	return m
}

// SortedMapping renders a mapping as deterministic "path -> id" lines.
func SortedMapping(m map[string]NodeID) []string {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		lines = append(lines, fmt.Sprintf("%s -> id=%d", p, m[p]))
	}
	return lines
}
