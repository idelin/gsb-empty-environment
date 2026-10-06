// Package dirindex maintains an in-memory directory index that reports
// per-subtree disk usage with hardlink-aware (inode) deduplication.
//
// Dedup model: every subtree deduplicates independently. The usage of a
// directory is the sum of the sizes of the distinct inodes that have at
// least one hard link anywhere beneath that directory. Consequently a
// parent's usage is generally NOT the sum of its children's usages: an
// inode linked into two sibling subtrees is counted once at the parent
// but once in each child.
//
// The index maintains usage incrementally. Each directory keeps a
// refcount map of inode -> number of links within its own subtree, so
// file additions and removals propagate up the ancestor chain in
// O(depth), and moving a directory re-propagates the moved subtree's
// inode multiset through both the old and the new ancestor chains.
package dirindex

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

var (
	ErrNotAbsolute       = errors.New("dirindex: path must be absolute")
	ErrNotFound          = errors.New("dirindex: path not found")
	ErrNotDir            = errors.New("dirindex: not a directory")
	ErrExist             = errors.New("dirindex: name already exists")
	ErrInvalidName       = errors.New("dirindex: invalid name")
	ErrMoveRoot          = errors.New("dirindex: cannot move the root directory")
	ErrMoveIntoSelf      = errors.New("dirindex: cannot move a directory into itself")
	ErrInodeSizeConflict = errors.New("dirindex: inode registered with a different size")
)

type node struct {
	name     string
	parent   *node
	children map[string]*node  // subdirectories
	files    map[string]uint64 // file name -> inode
	refs     map[uint64]int    // inode -> number of links within this subtree
	usage    int64             // sum of sizes of inodes with refs > 0
}

// Index is a directory index rooted at "/".
type Index struct {
	root  *node
	sizes map[uint64]int64 // inode -> file size (filesystem-global)
}

// New returns an empty index containing only the root directory "/".
func New() *Index {
	return &Index{
		root: &node{
			name:     "/",
			children: make(map[string]*node),
			files:    make(map[string]uint64),
			refs:     make(map[uint64]int),
		},
		sizes: make(map[uint64]int64),
	}
}

func splitPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, ErrNotAbsolute
	}
	p = path.Clean(p)
	if p == "/" {
		return nil, nil
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/"), nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}

func (ix *Index) lookup(p string) (*node, error) {
	segs, err := splitPath(p)
	if err != nil {
		return nil, err
	}
	cur := ix.root
	for _, seg := range segs {
		next, ok := cur.children[seg]
		if !ok {
			if _, isFile := cur.files[seg]; isFile {
				return nil, ErrNotDir
			}
			return nil, ErrNotFound
		}
		cur = next
	}
	return cur, nil
}

// Mkdir creates the directory at p, creating missing parents as needed.
func (ix *Index) Mkdir(p string) error {
	segs, err := splitPath(p)
	if err != nil {
		return err
	}
	cur := ix.root
	for _, seg := range segs {
		if !validName(seg) {
			return ErrInvalidName
		}
		if _, isFile := cur.files[seg]; isFile {
			return ErrNotDir
		}
		next, ok := cur.children[seg]
		if !ok {
			next = &node{
				name:     seg,
				parent:   cur,
				children: make(map[string]*node),
				files:    make(map[string]uint64),
				refs:     make(map[uint64]int),
			}
			cur.children[seg] = next
		}
		cur = next
	}
	return nil
}

// AddFile records a hard link: file name inside directory dir, pointing
// at inode with the given size. If the inode is already known, size must
// match the previously registered size.
func (ix *Index) AddFile(dir, name string, inode uint64, size int64) error {
	if !validName(name) {
		return ErrInvalidName
	}
	dirNode, err := ix.lookup(dir)
	if err != nil {
		return err
	}
	if _, ok := dirNode.files[name]; ok {
		return ErrExist
	}
	if _, ok := dirNode.children[name]; ok {
		return ErrExist
	}
	if prev, ok := ix.sizes[inode]; ok && prev != size {
		return ErrInodeSizeConflict
	}
	ix.sizes[inode] = size
	dirNode.files[name] = inode
	ix.propagate(dirNode, inode, +1)
	return nil
}

// RemoveFile removes the link dir/name. When the removed link was the
// last one to its inode within some ancestor subtree, that subtree's
// usage drops by the inode's size; when it was the last link in the
// whole index, the inode itself is forgotten.
func (ix *Index) RemoveFile(dir, name string) error {
	dirNode, err := ix.lookup(dir)
	if err != nil {
		return err
	}
	inode, ok := dirNode.files[name]
	if !ok {
		return ErrNotFound
	}
	delete(dirNode.files, name)
	ix.propagate(dirNode, inode, -1)
	if ix.root.refs[inode] == 0 {
		delete(ix.sizes, inode)
	}
	return nil
}

// Move relocates the directory src into the directory dstDir, keeping
// src's base name. All ancestor summaries on both the old and the new
// chain are updated. The root's usage is invariant under Move.
func (ix *Index) Move(src, dstDir string) error {
	srcNode, err := ix.lookup(src)
	if err != nil {
		return err
	}
	if srcNode == ix.root {
		return ErrMoveRoot
	}
	dstNode, err := ix.lookup(dstDir)
	if err != nil {
		return err
	}
	for cur := dstNode; cur != nil; cur = cur.parent {
		if cur == srcNode {
			return ErrMoveIntoSelf
		}
	}
	name := srcNode.name
	if _, ok := dstNode.children[name]; ok {
		return ErrExist
	}
	if _, ok := dstNode.files[name]; ok {
		return ErrExist
	}

	moved := make(map[uint64]int)
	collectRefs(srcNode, moved)

	ix.propagateAll(srcNode.parent, moved, -1)
	delete(srcNode.parent.children, name)
	srcNode.parent = dstNode
	dstNode.children[name] = srcNode
	ix.propagateAll(dstNode, moved, +1)
	return nil
}

// Usage returns the deduplicated subtree usage of the directory at p,
// as maintained incrementally by the index.
func (ix *Index) Usage(p string) (int64, error) {
	n, err := ix.lookup(p)
	if err != nil {
		return 0, err
	}
	return n.usage, nil
}

// ModelUsage recomputes the usage of the directory at p from scratch,
// walking its subtree with a fresh inode set. It is the independent
// reference model used to cross-check the incrementally maintained
// Usage values.
func (ix *Index) ModelUsage(p string) (int64, error) {
	n, err := ix.lookup(p)
	if err != nil {
		return 0, err
	}
	seen := make(map[uint64]struct{})
	var total int64
	var walk func(cur *node)
	walk = func(cur *node) {
		for _, inode := range cur.files {
			if _, ok := seen[inode]; !ok {
				seen[inode] = struct{}{}
				total += ix.sizes[inode]
			}
		}
		for _, child := range cur.children {
			walk(child)
		}
	}
	walk(n)
	return total, nil
}

// Paths returns the sorted paths of all directories in the index.
func (ix *Index) Paths() []string {
	var out []string
	var walk func(cur *node, prefix string)
	walk = func(cur *node, prefix string) {
		out = append(out, prefix)
		for name, child := range cur.children {
			walk(child, path.Join(prefix, name))
		}
	}
	walk(ix.root, "/")
	sort.Strings(out)
	return out
}

// Check verifies, for every directory in the index, that the
// incrementally maintained usage equals the reference model usage.
// It returns a descriptive error on the first mismatch.
func (ix *Index) Check() error {
	for _, p := range ix.Paths() {
		got, err := ix.Usage(p)
		if err != nil {
			return err
		}
		want, err := ix.ModelUsage(p)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("dirindex: usage mismatch at %s: index=%d model=%d", p, got, want)
		}
	}
	return nil
}

// Snapshot is an immutable point-in-time copy of every directory's
// usage in the index.
type Snapshot map[string]int64

// Snapshot records the current usage of every directory. Later
// mutations of the index do not affect the returned Snapshot.
func (ix *Index) Snapshot() Snapshot {
	snap := make(Snapshot)
	var walk func(cur *node, prefix string)
	walk = func(cur *node, prefix string) {
		snap[prefix] = cur.usage
		for name, child := range cur.children {
			walk(child, path.Join(prefix, name))
		}
	}
	walk(ix.root, "/")
	return snap
}

// Usage returns the recorded usage of path p and whether p existed in
// the index when the snapshot was taken.
func (s Snapshot) Usage(p string) (int64, bool) {
	v, ok := s[p]
	return v, ok
}

// propagate adds delta (+1 or -1) to the link count of inode for start
// and all of its ancestors, adjusting their usage on 0->1 and 1->0
// transitions.
func (ix *Index) propagate(start *node, inode uint64, delta int) {
	for cur := start; cur != nil; cur = cur.parent {
		if delta > 0 {
			if cur.refs[inode] == 0 {
				cur.usage += ix.sizes[inode]
			}
			cur.refs[inode]++
		} else {
			cur.refs[inode]--
			if cur.refs[inode] == 0 {
				delete(cur.refs, inode)
				cur.usage -= ix.sizes[inode]
			}
		}
	}
}

// propagateAll applies a whole inode multiset (the links of a moved
// subtree) to start and all of its ancestors. sign is +1 when the
// subtree is attached under start and -1 when it is detached.
func (ix *Index) propagateAll(start *node, m map[uint64]int, sign int) {
	for cur := start; cur != nil; cur = cur.parent {
		for inode, count := range m {
			if sign > 0 {
				if cur.refs[inode] == 0 {
					cur.usage += ix.sizes[inode]
				}
				cur.refs[inode] += count
			} else {
				cur.refs[inode] -= count
				if cur.refs[inode] == 0 {
					delete(cur.refs, inode)
					cur.usage -= ix.sizes[inode]
				}
			}
		}
	}
}

// collectRefs accumulates the inode multiset of the subtree rooted at n.
func collectRefs(n *node, out map[uint64]int) {
	for _, inode := range n.files {
		out[inode]++
	}
	for _, child := range n.children {
		collectRefs(child, out)
	}
}
