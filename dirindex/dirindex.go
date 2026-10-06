// Package dirindex maintains a directory-tree index where every directory
// tracks the disk usage of its subtree with hard links deduplicated by inode.
//
// Each subtree deduplicates independently: usage of a directory is the sum of
// the sizes of the distinct inodes reachable beneath it, never the sum of its
// children's usages (a hard link shared by two siblings must be counted once
// at their common ancestor).
//
// To make moves cheap and exact, every directory keeps an inode -> link-count
// map covering its whole subtree. Usage is derived from that map: an inode
// contributes its size exactly when its count is positive. Moving a directory
// subtracts the moved subtree's aggregate map from every old ancestor and
// adds it to every new ancestor, so all ancestor summaries stay correct
// without rescanning.
package dirindex

import (
	"fmt"
	"strings"
)

// File is a single directory entry (one hard link) pointing at an inode.
type File struct {
	Name  string
	Inode uint64
	Size  int64
}

// Dir is a directory node. links counts, for each inode, how many directory
// entries anywhere in this subtree point at it; usage is the deduplicated
// subtree size derived from links.
type Dir struct {
	Name     string
	parent   *Dir
	children map[string]*Dir
	files    map[string]*File
	links    map[uint64]int
	usage    int64
}

// Usage returns the inode-deduplicated size of the directory's subtree.
func (d *Dir) Usage() int64 { return d.usage }

// Index is the whole tree plus the inode -> size table for live inodes.
type Index struct {
	root  *Dir
	sizes map[uint64]int64
}

// New returns an empty index containing only the root directory.
func New() *Index {
	return &Index{
		root:  newDir(""),
		sizes: make(map[uint64]int64),
	}
}

func newDir(name string) *Dir {
	return &Dir{
		Name:     name,
		children: make(map[string]*Dir),
		files:    make(map[string]*File),
		links:    make(map[uint64]int),
	}
}

// Root returns the root directory.
func (ix *Index) Root() *Dir { return ix.root }

func splitPath(path string) []string {
	var parts []string
	for _, p := range strings.Split(path, "/") {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	return parts
}

// Lookup resolves path to a directory, or nil if it does not exist.
func (ix *Index) Lookup(path string) *Dir {
	d := ix.root
	for _, p := range splitPath(path) {
		next, ok := d.children[p]
		if !ok {
			return nil
		}
		d = next
	}
	return d
}

// UsageAt returns the deduplicated subtree usage of the directory at path.
func (ix *Index) UsageAt(path string) (int64, error) {
	d := ix.Lookup(path)
	if d == nil {
		return 0, fmt.Errorf("dirindex: no such directory %q", path)
	}
	return d.usage, nil
}

// MkdirAll creates the directory at path and any missing parents.
func (ix *Index) MkdirAll(path string) (*Dir, error) {
	d := ix.root
	for _, p := range splitPath(path) {
		next, ok := d.children[p]
		if !ok {
			next = newDir(p)
			next.parent = d
			d.children[p] = next
		}
		d = next
	}
	return d, nil
}

// AddFile links a new directory entry name -> inode with the given size into
// the directory at dirPath. All ancestors of that directory absorb the inode
// into their subtree aggregate; only the first link of an inode within a
// subtree grows that subtree's usage.
func (ix *Index) AddFile(dirPath, name string, inode uint64, size int64) error {
	d := ix.Lookup(dirPath)
	if d == nil {
		return fmt.Errorf("dirindex: no such directory %q", dirPath)
	}
	if _, ok := d.files[name]; ok {
		return fmt.Errorf("dirindex: file %q already exists in %q", name, dirPath)
	}
	if _, ok := d.children[name]; ok {
		return fmt.Errorf("dirindex: %q is a directory in %q", name, dirPath)
	}
	if s, ok := ix.sizes[inode]; ok && s != size {
		return fmt.Errorf("dirindex: inode %d size conflict: %d != %d", inode, s, size)
	}
	ix.sizes[inode] = size
	d.files[name] = &File{Name: name, Inode: inode, Size: size}
	for a := d; a != nil; a = a.parent {
		if a.links[inode] == 0 {
			a.usage += size
		}
		a.links[inode]++
	}
	return nil
}

// RemoveFile unlinks name from the directory at dirPath. When the removed
// entry was the last link to its inode within some ancestor's subtree, that
// ancestor's usage shrinks by the inode size; when it was the last link in
// the whole index, the inode is forgotten entirely.
func (ix *Index) RemoveFile(dirPath, name string) error {
	d := ix.Lookup(dirPath)
	if d == nil {
		return fmt.Errorf("dirindex: no such directory %q", dirPath)
	}
	f, ok := d.files[name]
	if !ok {
		return fmt.Errorf("dirindex: no such file %q in %q", name, dirPath)
	}
	delete(d.files, name)
	for a := d; a != nil; a = a.parent {
		a.links[f.Inode]--
		if a.links[f.Inode] == 0 {
			delete(a.links, f.Inode)
			a.usage -= f.Size
		}
	}
	if ix.root.links[f.Inode] == 0 {
		delete(ix.sizes, f.Inode)
	}
	return nil
}

// Rmdir removes the empty directory at path.
func (ix *Index) Rmdir(path string) error {
	d := ix.Lookup(path)
	if d == nil || d == ix.root {
		return fmt.Errorf("dirindex: cannot remove %q", path)
	}
	if len(d.children) != 0 || len(d.files) != 0 {
		return fmt.Errorf("dirindex: directory %q not empty", path)
	}
	delete(d.parent.children, d.Name)
	return nil
}

// Move relocates the directory at srcPath under the directory at
// newParentPath with name newName. The moved subtree's aggregate inode map is
// subtracted from every old ancestor and added to every new ancestor, so all
// ancestor summaries are updated exactly, including inodes shared by hard
// links across the move boundary.
func (ix *Index) Move(srcPath, newParentPath, newName string) error {
	src := ix.Lookup(srcPath)
	if src == nil || src == ix.root {
		return fmt.Errorf("dirindex: cannot move %q", srcPath)
	}
	np := ix.Lookup(newParentPath)
	if np == nil {
		return fmt.Errorf("dirindex: no such directory %q", newParentPath)
	}
	if newName == "" {
		newName = src.Name
	}
	if _, ok := np.children[newName]; ok {
		return fmt.Errorf("dirindex: %q already exists in %q", newName, newParentPath)
	}
	if _, ok := np.files[newName]; ok {
		return fmt.Errorf("dirindex: %q is a file in %q", newName, newParentPath)
	}
	for a := np; a != nil; a = a.parent {
		if a == src {
			return fmt.Errorf("dirindex: cannot move %q into its own subtree", srcPath)
		}
	}

	// Detach: subtract the subtree aggregate from every strict old ancestor.
	for a := src.parent; a != nil; a = a.parent {
		for ino, c := range src.links {
			a.links[ino] -= c
			if a.links[ino] == 0 {
				delete(a.links, ino)
				a.usage -= ix.sizes[ino]
			}
		}
	}
	delete(src.parent.children, src.Name)

	// Attach: add the subtree aggregate to every strict new ancestor.
	src.Name = newName
	src.parent = np
	np.children[newName] = src
	for a := np; a != nil; a = a.parent {
		for ino, c := range src.links {
			if a.links[ino] == 0 {
				a.usage += ix.sizes[ino]
			}
			a.links[ino] += c
		}
	}
	return nil
}

// Snapshot captures an immutable deep copy of the tree. Later mutations of
// the index never affect the snapshot, and vice versa.
func (ix *Index) Snapshot() *Snapshot {
	return &Snapshot{root: cloneDir(ix.root, nil)}
}

// Snapshot is a frozen point-in-time view of an Index.
type Snapshot struct {
	root *Dir
}

// UsageAt reports the deduplicated subtree usage of path as captured.
func (s *Snapshot) UsageAt(path string) (int64, error) {
	d := s.root
	for _, p := range splitPath(path) {
		next, ok := d.children[p]
		if !ok {
			return 0, fmt.Errorf("dirindex: no such directory %q in snapshot", path)
		}
		d = next
	}
	return d.usage, nil
}

func cloneDir(d *Dir, parent *Dir) *Dir {
	c := &Dir{
		Name:     d.Name,
		parent:   parent,
		children: make(map[string]*Dir, len(d.children)),
		files:    make(map[string]*File, len(d.files)),
		links:    make(map[uint64]int, len(d.links)),
		usage:    d.usage,
	}
	for name, f := range d.files {
		cp := *f
		c.files[name] = &cp
	}
	for ino, n := range d.links {
		c.links[ino] = n
	}
	for name, child := range d.children {
		c.children[name] = cloneDir(child, c)
	}
	return c
}
