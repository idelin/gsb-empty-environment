// Package vfs provides an in-memory, concurrency-safe model of a host
// filesystem directory graph.
//
// The graph is made of inode-like [Node] values connected through parent and
// children pointers. It models regular files, directories, static symbolic
// links (absolute or relative targets) and procfs-style magic links whose
// target is evaluated dynamically at access time.
//
// All mutating operations take the appropriate per-directory locks so that a
// resolver holding a stale node pointer can never observe a torn map: node
// pointers remain valid for the lifetime of the node even when it is renamed
// or detached, mirroring the RCU-style guarantee the kernel gives to path
// walkers.
package vfs

import (
	"errors"
	"io/fs"
	"sync"
	"sync/atomic"
)

// Kind identifies the concrete type of a [Node].
type Kind uint8

const (
	// KindDir is a directory node.
	KindDir Kind = iota
	// KindFile is a regular file node.
	KindFile
	// KindSymlink is a static symbolic link. Its target string is interpreted
	// by the path resolver, which decides how absolute targets are anchored.
	KindSymlink
	// KindMagicLink is a procfs-style magic symbolic link (for example
	// /proc/self/fd/3). Its target is recomputed on every access.
	KindMagicLink
)

// Common errors returned by VFS operations.
var (
	ErrExist    = errors.New("vfs: node already exists")
	ErrNotExist = errors.New("vfs: no such file or directory")
	ErrNotDir   = errors.New("vfs: not a directory")
	ErrIsDir    = errors.New("vfs: is a directory")
	ErrBadName  = errors.New("vfs: invalid path component")
)

var nextNodeID atomic.Uint64

// Node is an inode-like entry in the virtual filesystem.
//
// The mutex guards the children map and the parent/name linkage. Fields set
// at creation time (kind, target, magic) are immutable.
type Node struct {
	mu       sync.RWMutex
	id       uint64
	kind     Kind
	name     string
	parent   *Node
	children map[string]*Node

	data []byte
	mode fs.FileMode
	uid  uint32
	gid  uint32

	target string
	magic  MagicLink
}

// ID returns the stable, unique identifier of the node.
func (n *Node) ID() uint64 { return n.id }

// Kind returns the node kind.
func (n *Node) Kind() Kind { return n.kind }

// Name returns the base name of the node inside its parent directory.
func (n *Node) Name() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.name
}

// Parent returns the parent directory, or nil for a detached/root node.
func (n *Node) Parent() *Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.parent
}

// UID returns the owner UID stored on the node.
func (n *Node) UID() uint32 { return n.uid }

// GID returns the owner GID stored on the node.
func (n *Node) GID() uint32 { return n.gid }

// Mode returns the permission bits stored on the node.
func (n *Node) Mode() fs.FileMode { return n.mode }

// Target returns the static target of a KindSymlink node.
func (n *Node) Target() string { return n.target }

// Magic returns the dynamic target evaluator of a KindMagicLink node.
func (n *Node) Magic() MagicLink { return n.magic }

// Data returns a copy of the regular file contents.
func (n *Node) Data() []byte {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]byte, len(n.data))
	copy(out, n.data)
	return out
}

// SetData replaces the contents of a regular file.
func (n *Node) SetData(data []byte) error {
	if n.kind != KindFile {
		return ErrIsDir
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.data = append([]byte(nil), data...)
	return nil
}

// Child looks up a direct child by name. The returned node pointer remains
// valid after the lock is released.
func (n *Node) Child(name string) (*Node, error) {
	if n.kind != KindDir {
		return nil, ErrNotDir
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	child, ok := n.children[name]
	if !ok {
		return nil, ErrNotExist
	}
	return child, nil
}

// SnapshotChildren returns the current child entries keyed by name.
func (n *Node) SnapshotChildren() map[string]*Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make(map[string]*Node, len(n.children))
	for name, child := range n.children {
		out[name] = child
	}
	return out
}

func newNode(kind Kind, name string, mode fs.FileMode, uid, gid uint32) *Node {
	return &Node{
		id:   nextNodeID.Add(1),
		kind: kind,
		name: name,
		mode: mode,
		uid:  uid,
		gid:  gid,
	}
}
