// Package vfs resolves import-style paths over a virtual directory tree.
//
// Rules:
//   - Only virtual directories exist; there is no host filesystem access.
//   - The path separator is '/'. Backslash is an ordinary name character.
//   - Dot segments ("." and "..") are eliminated lexically; any ".." that
//     would climb above the root is rejected with ErrEscapesRoot.
//   - Symlinks are resolved component by component using directory identity
//     (inode pointers), never string prefix matching. At most MaxHops symlink
//     traversals are allowed per resolution; revisiting the same symlink
//     inode is reported as ErrLinkLoop.
//   - Containment is verified by walking parent pointers from the resolved
//     inode up to the root inode, so a path cannot escape by spelling tricks.
//   - Mutations are copy-on-write: replacing a node creates a fresh inode and
//     atomically swaps the parent's child map. Resolution is lock-free and
//     re-validates every traversed (dir, name, child-inode) step; if the
//     graph changed mid-walk (a replacement race) the resolution is retried.
package vfs

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxHops is the maximum number of symlink traversals in one resolution.
const MaxHops = 40

const (
	maxRetries = 128
	maxDepth   = 1 << 20
)

var (
	ErrEscapesRoot  = errors.New("vfs: path escapes root")
	ErrTooManyLinks = errors.New("vfs: too many symlink hops")
	ErrLinkLoop     = errors.New("vfs: symlink loop detected")
	ErrNotExist     = errors.New("vfs: path does not exist")
	ErrNotDir       = errors.New("vfs: component is not a directory")
	ErrExist        = errors.New("vfs: path already exists")
	ErrNotAbsolute  = errors.New("vfs: path must be absolute")
	ErrRace         = errors.New("vfs: resolution unstable under concurrent replacement")
)

type nodeKind uint8

const (
	dirNode nodeKind = iota
	fileNode
	symlinkNode
)

// node is immutable after creation except for a directory's child map, which
// is swapped atomically (copy-on-write). Replacement therefore always
// produces a new inode, so identity checks stay meaningful under races.
type node struct {
	ino    uint64
	kind   nodeKind
	name   string
	parent *node  // immutable; root's parent is itself
	target string // symlink target, immutable
	kids   atomic.Value
}

func (n *node) children() map[string]*node {
	if v := n.kids.Load(); v != nil {
		return v.(map[string]*node)
	}
	return nil
}

// FS is a virtual inode graph rooted at "/".
type FS struct {
	root *node
	seq  uint64
	mu   sync.Mutex // serializes mutations; resolution is lock-free
}

// New returns an empty filesystem containing only the root directory.
func New() *FS {
	fs := &FS{}
	root := &node{kind: dirNode, name: "/"}
	root.ino = fs.nextIno()
	root.parent = root
	root.kids.Store(map[string]*node{})
	fs.root = root
	return fs
}

func (fs *FS) nextIno() uint64 { return atomic.AddUint64(&fs.seq, 1) }

// RootIno returns the inode number of the root directory.
func (fs *FS) RootIno() uint64 { return fs.root.ino }

// Result describes a resolved path.
type Result struct {
	Path  string // canonical path derived from inode identity
	Ino   uint64 // inode of the final node
	IsDir bool
}

// step records one (directory, name, child) traversal for post-walk
// validation against concurrent replacement.
type step struct {
	dir   *node
	name  string
	child *node
}

// Resolve resolves an absolute path to its final inode, following symlinks
// by identity. It retries if the graph is replaced mid-resolution.
func (fs *FS) Resolve(path string) (Result, error) {
	n, err := fs.resolveNode(path)
	if err != nil {
		return Result{}, err
	}
	return Result{Path: fs.pathOf(n), Ino: n.ino, IsDir: n.kind == dirNode}, nil
}

func (fs *FS) resolveNode(path string) (*node, error) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		final, steps, err := fs.resolveOnce(path)
		if err != nil {
			return nil, err
		}
		if !fs.contains(final) {
			return nil, ErrEscapesRoot
		}
		if validSteps(steps) {
			return final, nil
		}
	}
	return nil, ErrRace
}

// resolveOnce performs a single lock-free resolution attempt.
func (fs *FS) resolveOnce(path string) (*node, []step, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, nil, ErrNotAbsolute
	}
	comps := strings.Split(path, "/")
	var steps []step
	visited := make(map[uint64]struct{})
	hops := 0
	dir := fs.root
	final := fs.root

	for i := 0; i < len(comps); i++ {
		c := comps[i]
		switch c {
		case "", ".":
			continue
		case "..":
			if dir == fs.root {
				return nil, nil, ErrEscapesRoot
			}
			dir = dir.parent
			final = dir
			continue
		}

		child := dir.children()[c]
		if child == nil {
			return nil, nil, ErrNotExist
		}
		steps = append(steps, step{dir: dir, name: c, child: child})

		switch child.kind {
		case dirNode:
			dir = child
			final = child
		case fileNode:
			if i+1 < len(comps) {
				return nil, nil, ErrNotDir
			}
			final = child
		case symlinkNode:
			hops++
			if hops > MaxHops {
				return nil, nil, ErrTooManyLinks
			}
			if _, seen := visited[child.ino]; seen {
				return nil, nil, ErrLinkLoop
			}
			visited[child.ino] = struct{}{}
			tcomps := strings.Split(child.target, "/")
			if strings.HasPrefix(child.target, "/") {
				dir = fs.root
			}
			// Splice the target components ahead of the unconsumed input.
			rest := make([]string, 0, len(tcomps)+len(comps)-i-1)
			rest = append(rest, tcomps...)
			rest = append(rest, comps[i+1:]...)
			comps = rest
			i = -1
		}
	}
	return final, steps, nil
}

// validSteps re-checks every traversed edge against the current graph. If a
// directory was replaced mid-walk, some edge no longer holds and the caller
// retries, so a resolution never mixes pre- and post-replacement state.
func validSteps(steps []step) bool {
	for _, s := range steps {
		if s.dir.children()[s.name] != s.child {
			return false
		}
	}
	return true
}

// contains verifies containment by inode identity: it walks parent pointers
// from n and must reach the root. No string prefix comparison is involved.
func (fs *FS) contains(n *node) bool {
	for i := 0; i < maxDepth; i++ {
		if n == fs.root {
			return true
		}
		if n.parent == nil || n.parent == n {
			return false
		}
		n = n.parent
	}
	return false
}

// pathOf derives the canonical path of n from its parent chain.
func (fs *FS) pathOf(n *node) string {
	if n == fs.root {
		return "/"
	}
	var parts []string
	for cur := n; cur != fs.root; cur = cur.parent {
		parts = append(parts, cur.name)
	}
	for l, r := 0, len(parts)-1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	return "/" + strings.Join(parts, "/")
}

// cleanPath eliminates dot segments lexically and rejects escapes above root.
func cleanPath(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, ErrNotAbsolute
	}
	var out []string
	for _, c := range strings.Split(path, "/") {
		switch c {
		case "", ".":
		case "..":
			if len(out) == 0 {
				return nil, ErrEscapesRoot
			}
			out = out[:len(out)-1]
		default:
			out = append(out, c)
		}
	}
	return out, nil
}

func setChild(dir *node, name string, child *node) {
	old := dir.children()
	next := make(map[string]*node, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	next[name] = child
	dir.kids.Store(next)
}

// parentOf resolves the parent directory of path and returns it together
// with the final component name. Callers must hold fs.mu.
func (fs *FS) parentOf(path string) (*node, string, error) {
	comps, err := cleanPath(path)
	if err != nil {
		return nil, "", err
	}
	if len(comps) == 0 {
		return nil, "", ErrExist // the root itself
	}
	parent := fs.root
	if len(comps) > 1 {
		p, err := fs.resolveNode("/" + strings.Join(comps[:len(comps)-1], "/"))
		if err != nil {
			return nil, "", err
		}
		parent = p
	}
	if parent.kind != dirNode {
		return nil, "", ErrNotDir
	}
	return parent, comps[len(comps)-1], nil
}

// MkdirAll creates a directory and any missing parents. Existing symlink
// components are followed to their real directory.
func (fs *FS) MkdirAll(path string) error {
	comps, err := cleanPath(path)
	if err != nil {
		return err
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	cur := fs.root
	for _, c := range comps {
		child := cur.children()[c]
		if child != nil && child.kind == symlinkNode {
			n, err := fs.resolveNode(fs.pathOf(cur) + "/" + c)
			if err != nil {
				return err
			}
			if n.kind != dirNode {
				return ErrNotDir
			}
			child = n
		}
		if child == nil {
			child = &node{kind: dirNode, name: c, parent: cur, ino: fs.nextIno()}
			child.kids.Store(map[string]*node{})
			setChild(cur, c, child)
		}
		if child.kind != dirNode {
			return ErrNotDir
		}
		cur = child
	}
	return nil
}

// Create makes a regular file. It fails with ErrExist if path is taken.
func (fs *FS) Create(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	parent, name, err := fs.parentOf(path)
	if err != nil {
		return err
	}
	if parent.children()[name] != nil {
		return ErrExist
	}
	setChild(parent, name, &node{kind: fileNode, name: name, parent: parent, ino: fs.nextIno()})
	return nil
}

// Symlink creates a symbolic link at linkPath pointing to target. The target
// may be absolute or relative to the link's parent directory; it is not
// validated at creation time.
func (fs *FS) Symlink(target, linkPath string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	parent, name, err := fs.parentOf(linkPath)
	if err != nil {
		return err
	}
	if parent.children()[name] != nil {
		return ErrExist
	}
	setChild(parent, name, &node{kind: symlinkNode, name: name, parent: parent, target: target, ino: fs.nextIno()})
	return nil
}

// ReplaceWithDir atomically swaps the node at path for a fresh empty
// directory with a new inode.
func (fs *FS) ReplaceWithDir(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	parent, name, err := fs.parentOf(path)
	if err != nil {
		return err
	}
	child := &node{kind: dirNode, name: name, parent: parent, ino: fs.nextIno()}
	child.kids.Store(map[string]*node{})
	setChild(parent, name, child)
	return nil
}

// ReplaceWithSymlink atomically swaps the node at path for a fresh symlink
// with a new inode.
func (fs *FS) ReplaceWithSymlink(path, target string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	parent, name, err := fs.parentOf(path)
	if err != nil {
		return err
	}
	setChild(parent, name, &node{kind: symlinkNode, name: name, parent: parent, target: target, ino: fs.nextIno()})
	return nil
}
