// Package vfs implements an in-memory (virtual) directory tree with
// Linux-namei-style path resolution.
//
// Resolution rules:
//   - The path separator is '/'. Dot segments ("." and "..") are
//     eliminated during the walk; any ".." that would climb above the
//     root is rejected with ErrEscapesRoot.
//   - Symbolic links are expanded step by step, keyed on directory
//     inode identity (never on string prefixes). At most MaxSymlinkHops
//     expansions are allowed per resolution.
//   - Absolute symlink targets restart at the virtual root, so links
//     can never escape the jail.
package vfs

import (
	"errors"
	"strings"
	"sync"
)

// MaxSymlinkHops is the maximum number of symbolic-link expansions
// allowed during a single path resolution (mirrors Linux MAXSYMLINKS).
const MaxSymlinkHops = 40

var (
	ErrEscapesRoot  = errors.New("vfs: path escapes root")
	ErrTooManyLinks = errors.New("vfs: too many levels of symbolic links")
	ErrNotDir       = errors.New("vfs: not a directory")
	ErrNotExist     = errors.New("vfs: no such file or directory")
	ErrExist        = errors.New("vfs: file exists")
	ErrInvalidName  = errors.New("vfs: invalid path component")
)

// Kind identifies the type of a Node.
type Kind uint8

const (
	Dir Kind = iota
	File
	Symlink
)

// Node is an inode in the virtual file system. Identity (pointer
// equality) is what the resolver tracks; names are only used to look
// up children inside a directory.
type Node struct {
	kind     Kind
	name     string
	parent   *Node
	target   string           // Symlink only
	children map[string]*Node // Dir only
}

// Kind reports the node type.
func (n *Node) Kind() Kind { return n.kind }

// Name reports the node's final path component.
func (n *Node) Name() string { return n.name }

// Target reports a symlink's raw target string.
func (n *Node) Target() string { return n.target }

// FS is a virtual file system rooted at a single directory inode.
type FS struct {
	mu   sync.RWMutex
	root *Node

	// preStep, when non-nil, runs before each component lookup during
	// resolution. It exists so tests can mutate the tree mid-walk and
	// verify replacement-race behaviour.
	preStep func()
}

// New returns an empty virtual file system.
func New() *FS {
	return &FS{root: &Node{kind: Dir, name: "/", children: map[string]*Node{}}}
}

// Root returns the root inode.
func (fs *FS) Root() *Node { return fs.root }

func validName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.Contains(s, "/")
}

// splitPath splits a path into its parent path and final component.
func splitPath(path string) (dir, base string) {
	path = strings.TrimRight(path, "/")
	i := strings.LastIndexByte(path, '/')
	switch {
	case i < 0:
		return "/", path
	case i == 0:
		return "/", path[1:]
	default:
		return path[:i], path[i+1:]
	}
}

// lookup fetches the child of dir named name under a read lock, so a
// concurrent Replace can swap the mapping between two steps of a walk.
func (fs *FS) lookup(dir *Node, name string) *Node {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if dir.kind != Dir {
		return nil
	}
	return dir.children[name]
}

// create inserts a fresh node as the final component of
// parentPath/name, failing if the name is taken.
func (fs *FS) create(parentPath, name string, kind Kind, target string) (*Node, error) {
	if !validName(name) {
		return nil, ErrInvalidName
	}
	parent, err := fs.resolve(fs.root, parentPath)
	if err != nil {
		return nil, err
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if parent.kind != Dir {
		return nil, ErrNotDir
	}
	if _, ok := parent.children[name]; ok {
		return nil, ErrExist
	}
	n := &Node{kind: kind, name: name, parent: parent, target: target}
	if kind == Dir {
		n.children = map[string]*Node{}
	}
	parent.children[name] = n
	return n, nil
}

// MkdirAll creates path and any missing parents as directories.
// Existing components are resolved (symlinks followed).
func (fs *FS) MkdirAll(path string) (*Node, error) {
	var node *Node
	cur := "/"
	for _, c := range strings.Split(path, "/") {
		if c == "" || c == "." {
			continue
		}
		next := cur
		if !strings.HasSuffix(next, "/") {
			next += "/"
		}
		next += c
		n, err := fs.resolve(fs.root, next)
		if err == ErrNotExist {
			n, err = fs.create(cur, c, Dir, "")
		}
		if err != nil {
			return nil, err
		}
		if n.kind != Dir {
			return nil, ErrNotDir
		}
		node = n
		cur = next
	}
	if node == nil {
		return fs.root, nil
	}
	return node, nil
}

// AddFile creates a regular file at path.
func (fs *FS) AddFile(path string) (*Node, error) {
	dir, base := splitPath(path)
	return fs.create(dir, base, File, "")
}

// AddSymlink creates a symbolic link at path pointing to target.
func (fs *FS) AddSymlink(path, target string) (*Node, error) {
	if target == "" {
		return nil, ErrInvalidName
	}
	dir, base := splitPath(path)
	return fs.create(dir, base, Symlink, target)
}

// Replace atomically swaps the node at path for a fresh node of the
// given kind. It models a concurrent rename/unlink racing a walk.
func (fs *FS) Replace(path string, kind Kind, target string) (*Node, error) {
	dir, base := splitPath(path)
	if !validName(base) {
		return nil, ErrInvalidName
	}
	parent, err := fs.resolve(fs.root, dir)
	if err != nil {
		return nil, err
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if parent.kind != Dir {
		return nil, ErrNotDir
	}
	n := &Node{kind: kind, name: base, parent: parent, target: target}
	if kind == Dir {
		n.children = map[string]*Node{}
	}
	parent.children[base] = n
	return n, nil
}

// Path reconstructs the absolute path of n by climbing parent
// pointers. The boolean reports whether n is reachable from the root.
func (fs *FS) Path(n *Node) (string, bool) {
	if n == nil {
		return "", false
	}
	var parts []string
	for cur := n; cur != fs.root; cur = cur.parent {
		if cur == nil {
			return "", false
		}
		parts = append(parts, cur.name)
	}
	var b strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteByte('/')
		b.WriteString(parts[i])
	}
	if b.Len() == 0 {
		return "/", true
	}
	return b.String(), true
}
