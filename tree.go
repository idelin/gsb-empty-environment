package main

import (
	"fmt"
	"sort"
	"strings"
)

// Node is a file-system object with a stable identity (ID).
// Its path is derived from its current position in the tree and
// is never stored as identity.
type Node struct {
	ID       int64
	Name     string
	IsDir    bool
	Parent   *Node
	Children map[string]*Node
}

// Tree is a synthetic file tree rooted at a synthetic root node.
type Tree struct {
	root *Node
	byID map[int64]*Node
}

func NewTree() *Tree {
	root := &Node{ID: 0, Name: "", IsDir: true, Children: map[string]*Node{}}
	return &Tree{root: root, byID: map[int64]*Node{0: root}}
}

func splitPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("path %q is not absolute", p)
	}
	var parts []string
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return nil, fmt.Errorf("path %q contains '..'", p)
		}
		parts = append(parts, seg)
	}
	return parts, nil
}

// resolve walks the tree and returns the node at path p.
func (t *Tree) resolve(p string) (*Node, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, err
	}
	cur := t.root
	for _, seg := range parts {
		if !cur.IsDir {
			return nil, fmt.Errorf("%q: not a directory", cur.Name)
		}
		next, ok := cur.Children[seg]
		if !ok {
			return nil, fmt.Errorf("path %q does not exist", p)
		}
		cur = next
	}
	return cur, nil
}

// resolveParent returns the parent directory and final component of p.
func (t *Tree) resolveParent(p string) (*Node, string, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, "", err
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("operation on root is not allowed")
	}
	parent, err := t.resolve("/" + strings.Join(parts[:len(parts)-1], "/"))
	if err != nil {
		return nil, "", err
	}
	if !parent.IsDir {
		return nil, "", fmt.Errorf("parent of %q is not a directory", p)
	}
	return parent, parts[len(parts)-1], nil
}

// Path computes the current absolute path of n.
func (t *Tree) Path(n *Node) string {
	if n == t.root {
		return "/"
	}
	var segs []string
	for cur := n; cur != t.root; cur = cur.Parent {
		segs = append(segs, cur.Name)
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return "/" + strings.Join(segs, "/")
}

// isDescendant reports whether n is d or lies inside d's subtree.
func isDescendant(n, d *Node) bool {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur == d {
			return true
		}
	}
	return false
}

// Create makes a new node with the given stable ID at path p.
// All checks run before any mutation, so a failure changes nothing.
func (t *Tree) Create(p string, id int64, isDir bool) error {
	if id == 0 {
		return fmt.Errorf("create %q: id 0 is reserved for root", p)
	}
	if _, taken := t.byID[id]; taken {
		return fmt.Errorf("create %q: id %d already in use", p, id)
	}
	parent, name, err := t.resolveParent(p)
	if err != nil {
		return fmt.Errorf("create %q: %w", p, err)
	}
	if _, exists := parent.Children[name]; exists {
		return fmt.Errorf("create %q: name already exists", p)
	}
	n := &Node{ID: id, Name: name, IsDir: isDir, Parent: parent}
	if isDir {
		n.Children = map[string]*Node{}
	}
	parent.Children[name] = n
	t.byID[id] = n
	return nil
}

// Unlink removes the node at path p. Directories must be empty.
func (t *Tree) Unlink(p string) error {
	n, err := t.resolve(p)
	if err != nil {
		return fmt.Errorf("unlink %q: %w", p, err)
	}
	if n == t.root {
		return fmt.Errorf("unlink: cannot remove root")
	}
	if n.IsDir && len(n.Children) > 0 {
		return fmt.Errorf("unlink %q: directory not empty", p)
	}
	delete(n.Parent.Children, n.Name)
	delete(t.byID, n.ID)
	n.Parent = nil
	return nil
}

// renamePlan captures a fully validated rename so commit cannot fail.
type renamePlan struct {
	src       *Node
	newParent *Node
	newName   string
	replaced  *Node // existing target to unlink, nil if none
}

// planRename validates a rename without mutating the tree.
func (t *Tree) planRename(oldPath, newPath string) (*renamePlan, error) {
	src, err := t.resolve(oldPath)
	if err != nil {
		return nil, fmt.Errorf("rename %q: %w", oldPath, err)
	}
	if src == t.root {
		return nil, fmt.Errorf("rename: cannot move root")
	}
	newParent, newName, err := t.resolveParent(newPath)
	if err != nil {
		return nil, fmt.Errorf("rename %q -> %q: %w", oldPath, newPath, err)
	}
	// Cycle prevention: a directory can never be moved into itself
	// or into one of its own descendants.
	if src.IsDir && isDescendant(newParent, src) {
		return nil, fmt.Errorf("rename %q -> %q: would create a parent cycle", oldPath, newPath)
	}
	plan := &renamePlan{src: src, newParent: newParent, newName: newName}
	if target, exists := newParent.Children[newName]; exists {
		if target == src {
			// rename onto itself: treat as a no-op plan.
			return &renamePlan{src: src, newParent: src.Parent, newName: src.Name}, nil
		}
		// POSIX-style replacement rules.
		switch {
		case target.IsDir && !src.IsDir:
			return nil, fmt.Errorf("rename %q -> %q: cannot replace directory with file", oldPath, newPath)
		case !target.IsDir && src.IsDir:
			return nil, fmt.Errorf("rename %q -> %q: cannot replace file with directory", oldPath, newPath)
		case target.IsDir && len(target.Children) > 0:
			return nil, fmt.Errorf("rename %q -> %q: target directory not empty", oldPath, newPath)
		}
		plan.replaced = target
	}
	return plan, nil
}

// commit applies a validated plan. It performs no checks and cannot fail.
func (t *Tree) commit(p *renamePlan) {
	if p.replaced != nil {
		delete(p.replaced.Parent.Children, p.replaced.Name)
		delete(t.byID, p.replaced.ID)
		p.replaced.Parent = nil
	}
	delete(p.src.Parent.Children, p.src.Name)
	p.src.Parent = p.newParent
	p.src.Name = p.newName
	p.newParent.Children[p.newName] = p.src
}

// Rename moves the node at oldPath to newPath, atomically replacing an
// existing target when permitted. Validation completes before any
// mutation, so a failed rename leaves the tree untouched.
func (t *Tree) Rename(oldPath, newPath string) error {
	plan, err := t.planRename(oldPath, newPath)
	if err != nil {
		return err
	}
	t.commit(plan)
	return nil
}

// Mapping returns the current path -> stable-ID view of the whole tree.
func (t *Tree) Mapping() map[string]int64 {
	out := map[string]int64{}
	var walk func(n *Node, path string)
	walk = func(n *Node, path string) {
		out[path] = n.ID
		if n.IsDir {
			keys := make([]string, 0, len(n.Children))
			for k := range n.Children {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				child := n.Children[k]
				walk(child, joinPath(path, child.Name))
			}
		}
	}
	walk(t.root, "/")
	return out
}

func joinPath(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}
