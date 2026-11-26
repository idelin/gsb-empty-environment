package vfs

import (
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// VFS is the global virtual filesystem graph. It owns node allocation and
// serializes structural mutations that cross directory boundaries.
type VFS struct {
	// HostRoot is the root of the simulated host filesystem ("/").
	HostRoot *Node

	mu sync.Mutex
}

// New constructs an empty VFS containing only the host root directory.
func New() *VFS {
	v := &VFS{}
	v.HostRoot = v.MkdirDetached("/", 0o755, 0, 0)
	v.HostRoot.name = "/"
	return v
}

// MkdirDetached creates a directory not connected to any parent. It is the
// primitive used to create fresh mount source roots.
func (v *VFS) MkdirDetached(name string, mode fs.FileMode, uid, gid uint32) *Node {
	return newNode(KindDir, name, mode.Perm()|fs.ModeDir, uid, gid)
}

// MkfileDetached creates a regular file not connected to any parent.
func (v *VFS) MkfileDetached(name string, data []byte, mode fs.FileMode, uid, gid uint32) *Node {
	node := newNode(KindFile, name, mode.Perm(), uid, gid)
	node.data = append([]byte(nil), data...)
	return node
}

// SymlinkDetached creates a static symbolic link not connected to any parent.
// target may be absolute ("/etc/shadow") or relative ("../../secret").
func (v *VFS) SymlinkDetached(name, target string, uid, gid uint32) *Node {
	node := newNode(KindSymlink, name, 0o777, uid, gid)
	node.target = target
	return node
}

// MagicLinkDetached creates a procfs-style magic symbolic link not connected
// to any parent.
func (v *VFS) MagicLinkDetached(name string, magic MagicLink, uid, gid uint32) *Node {
	node := newNode(KindMagicLink, name, 0o777, uid, gid)
	node.magic = magic
	return node
}

// Mkdir creates a directory under parent.
func (v *VFS) Mkdir(parent *Node, name string, mode fs.FileMode, uid, gid uint32) (*Node, error) {
	node := v.MkdirDetached(name, mode, uid, gid)
	if err := v.Link(parent, name, node); err != nil {
		return nil, err
	}
	return node, nil
}

// Mkfile creates a regular file under parent.
func (v *VFS) Mkfile(parent *Node, name string, data []byte, mode fs.FileMode, uid, gid uint32) (*Node, error) {
	node := v.MkfileDetached(name, data, mode, uid, gid)
	if err := v.Link(parent, name, node); err != nil {
		return nil, err
	}
	return node, nil
}

// Symlink creates a static symbolic link under parent.
func (v *VFS) Symlink(parent *Node, name, target string, uid, gid uint32) (*Node, error) {
	node := v.SymlinkDetached(name, target, uid, gid)
	if err := v.Link(parent, name, node); err != nil {
		return nil, err
	}
	return node, nil
}

// MagicLinkAt creates a magic symbolic link under parent.
func (v *VFS) MagicLinkAt(parent *Node, name string, magic MagicLink, uid, gid uint32) (*Node, error) {
	node := v.MagicLinkDetached(name, magic, uid, gid)
	if err := v.Link(parent, name, node); err != nil {
		return nil, err
	}
	return node, nil
}

// Link attaches an existing detached node to a directory under name.
func (v *VFS) Link(parent *Node, name string, node *Node) error {
	if parent.Kind() != KindDir {
		return ErrNotDir
	}
	if !validName(name) {
		return ErrBadName
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	parent.mu.Lock()
	defer parent.mu.Unlock()
	if parent.children == nil {
		parent.children = map[string]*Node{}
	}
	if _, exists := parent.children[name]; exists {
		return ErrExist
	}
	parent.children[name] = node
	node.mu.Lock()
	node.parent = parent
	node.name = name
	node.mu.Unlock()
	return nil
}

// Rename atomically moves srcName inside srcDir to dstName inside dstDir.
// If dstName already names a regular file or symlink it is replaced; an
// existing directory is never overwritten.
func (v *VFS) Rename(srcDir *Node, srcName string, dstDir *Node, dstName string) error {
	if !validName(srcName) || !validName(dstName) {
		return ErrBadName
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.renameLocked(srcDir, dstDir, srcName, dstName, false)
}

// Exchange atomically swaps two directory entries, even when both are
// directories. This models renameat2(RENAME_EXCHANGE), the primitive behind
// the classic symlink/swap TOCTOU attacks.
func (v *VFS) Exchange(aDir *Node, aName string, bDir *Node, bName string) error {
	if !validName(aName) || !validName(bName) {
		return ErrBadName
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.renameLocked(aDir, bDir, aName, bName, true)
}

func (v *VFS) renameLocked(srcDir, dstDir *Node, srcName, dstName string, exchange bool) error {
	if srcDir.Kind() != KindDir || dstDir.Kind() != KindDir {
		return ErrNotDir
	}
	locks := orderedDirLocks(srcDir, dstDir)
	for _, dir := range locks {
		dir.mu.Lock()
		defer dir.mu.Unlock()
	}
	src, ok := srcDir.children[srcName]
	if !ok {
		return ErrNotExist
	}
	dst, dstExists := dstDir.children[dstName]
	if exchange {
		if !dstExists {
			return ErrNotExist
		}
		srcDir.children[srcName], dstDir.children[dstName] = dst, src
		src.mu.Lock()
		dst.mu.Lock()
		// src now occupies the destination slot, dst the source slot.
		src.parent, src.name = dstDir, dstName
		dst.parent, dst.name = srcDir, srcName
		dst.mu.Unlock()
		src.mu.Unlock()
		return nil
	}
	if dstExists && dst.Kind() == KindDir {
		return ErrIsDir
	}
	delete(srcDir.children, srcName)
	if dstDir.children == nil {
		dstDir.children = map[string]*Node{}
	}
	dstDir.children[dstName] = src
	src.mu.Lock()
	src.parent = dstDir
	src.name = dstName
	src.mu.Unlock()
	return nil
}

// Remove unlinks an entry. Non-empty directories are rejected.
func (v *VFS) Remove(dir *Node, name string) error {
	if dir.Kind() != KindDir {
		return ErrNotDir
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	dir.mu.Lock()
	defer dir.mu.Unlock()
	node, ok := dir.children[name]
	if !ok {
		return ErrNotExist
	}
	if node.Kind() == KindDir && len(node.children) > 0 {
		return ErrIsDir
	}
	delete(dir.children, name)
	node.mu.Lock()
	node.parent = nil
	node.mu.Unlock()
	return nil
}

// PathOf returns the host-absolute path of a node by walking parent pointers.
// Detached nodes render as "<detached>/name".
func PathOf(n *Node) string {
	if n == nil {
		return ""
	}
	if n.Parent() == nil {
		if n.name == "/" {
			return "/"
		}
		return "<detached>/" + n.name
	}
	var parts []string
	for cur := n; cur != nil && cur.Parent() != nil; cur = cur.Parent() {
		parts = append(parts, cur.Name())
	}
	rev := make([]string, len(parts))
	for i, p := range parts {
		rev[len(parts)-1-i] = p
	}
	return "/" + strings.Join(rev, "/")
}

// IsAncestor reports whether ancestor is an ancestor of node (or the node
// itself) following parent pointers.
func IsAncestor(ancestor, node *Node) bool {
	for cur := node; cur != nil; cur = cur.Parent() {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// SortedNames returns child names in deterministic order.
func SortedNames(n *Node) []string {
	names := make([]string, 0, len(n.SnapshotChildren()))
	for name := range n.SnapshotChildren() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}

func orderedDirLocks(a, b *Node) []*Node {
	if a == b {
		return []*Node{a}
	}
	if a.id < b.id {
		return []*Node{a, b}
	}
	return []*Node{b, a}
}
