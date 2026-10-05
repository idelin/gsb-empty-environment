// Package dirstats maintains logical-byte and physical-unique-block-byte
// statistics for a directory tree whose files reference shared,
// deduplicated blocks.
//
// Logical bytes count every directory entry (hard links included), while
// physical bytes count each distinct block reachable from a subtree only
// once. Statistics are maintained incrementally: every mutation propagates
// its delta along the chain of affected ancestors. All mutating operations
// validate first and never leave partial updates behind.
package dirstats

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrNotFound      = errors.New("dirstats: entry not found")
	ErrExists        = errors.New("dirstats: entry already exists")
	ErrInvalidName   = errors.New("dirstats: invalid name")
	ErrCycle         = errors.New("dirstats: move would create a directory cycle")
	ErrCrossStore    = errors.New("dirstats: directories belong to different stores")
	ErrNegativeSize  = errors.New("dirstats: negative size")
	ErrBlockConflict = errors.New("dirstats: conflicting size for block")
	ErrNotFile       = errors.New("dirstats: entry is not a file")
	ErrNotDir        = errors.New("dirstats: entry is not a directory")
)

// BlockID identifies a physical data block. Blocks may be shared by any
// number of files across the tree.
type BlockID string

// Block is a reference to a physical block together with its size.
type Block struct {
	ID   BlockID
	Size int64
}

// delta is a signed-statistics payload propagated along ancestor chains.
// refs maps a block to the number of directory entries in the subtree that
// reference it; a block contributes to physical bytes while its count is
// positive.
type delta struct {
	logical int64
	refs    map[BlockID]int64
}

// node is a directory entry: either *Dir or *File.
type node interface {
	delta() delta
	setParent(parent *Dir, name string)
	clearParent()
}

// Store owns a directory tree and the registry of known block sizes.
type Store struct {
	root   *Dir
	blocks map[BlockID]int64
}

// NewStore returns an empty store with an empty root directory.
func NewStore() *Store {
	s := &Store{blocks: make(map[BlockID]int64)}
	s.root = &Dir{store: s, children: make(map[string]node), refs: make(map[BlockID]int64)}
	return s
}

// Root returns the root directory of the tree.
func (s *Store) Root() *Dir { return s.root }

// registerBlocks validates the given blocks against the registry and adds
// any new ones. It fails atomically: either all blocks are acceptable or
// none are registered.
func (s *Store) registerBlocks(blocks []Block) error {
	for _, b := range blocks {
		if b.Size < 0 {
			return fmt.Errorf("%w: block %q has size %d", ErrNegativeSize, b.ID, b.Size)
		}
		if size, ok := s.blocks[b.ID]; ok && size != b.Size {
			return fmt.Errorf("%w: block %q registered with size %d, got %d", ErrBlockConflict, b.ID, size, b.Size)
		}
	}
	for _, b := range blocks {
		s.blocks[b.ID] = b.Size
	}
	return nil
}

// Dir is a directory node maintaining aggregate statistics for its subtree.
type Dir struct {
	store    *Store
	name     string
	parent   *Dir
	children map[string]node
	logical  int64
	physical int64
	refs     map[BlockID]int64
}

// File is a file node. A file may be hard-linked into any number of
// directory entries; each entry contributes to logical bytes, while its
// blocks contribute to physical bytes only where not already reachable.
type File struct {
	store   *Store
	size    int64
	blocks  []Block
	entries []entry
}

// entry is one hard-link location of a file.
type entry struct {
	dir  *Dir
	name string
}

func (d *Dir) delta() delta {
	return delta{logical: d.logical, refs: d.refs}
}

func (d *Dir) setParent(parent *Dir, name string) { d.parent, d.name = parent, name }
func (d *Dir) clearParent()                       { d.parent, d.name = nil, "" }

func (f *File) delta() delta {
	dl := delta{logical: f.size, refs: make(map[BlockID]int64)}
	for _, b := range f.blocks {
		dl.refs[b.ID] = 1
	}
	return dl
}

func (f *File) setParent(parent *Dir, name string) {
	f.entries = append(f.entries, entry{dir: parent, name: name})
}

func (f *File) clearParent() { f.entries = nil }

func (f *File) removeEntry(dir *Dir, name string) {
	for i, e := range f.entries {
		if e.dir == dir && e.name == name {
			f.entries = append(f.entries[:i], f.entries[i+1:]...)
			return
		}
	}
}

// propagate applies sign*dl to dir and every ancestor, keeping physical
// bytes in sync with block reference counts.
func propagate(dir *Dir, dl delta, sign int64) {
	for cur := dir; cur != nil; cur = cur.parent {
		cur.logical += sign * dl.logical
		for id, n := range dl.refs {
			before := cur.refs[id]
			after := before + sign*n
			if after == 0 {
				delete(cur.refs, id)
			} else {
				cur.refs[id] = after
			}
			switch {
			case before == 0 && after > 0:
				cur.physical += cur.store.blocks[id]
			case before > 0 && after == 0:
				cur.physical -= cur.store.blocks[id]
			}
		}
	}
}

// attach links n under name and propagates its delta upward.
func (d *Dir) attach(name string, n node) {
	d.children[name] = n
	n.setParent(d, name)
	propagate(d, n.delta(), +1)
}

// detach unlinks the named entry and propagates its delta downward.
func (d *Dir) detach(name string) node {
	n := d.children[name]
	delete(d.children, name)
	propagate(d, n.delta(), -1)
	if f, ok := n.(*File); ok {
		f.removeEntry(d, name)
	} else {
		n.clearParent()
	}
	return n
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsRune(name, '/')
}

// Name returns the entry name of the directory ("" for the root).
func (d *Dir) Name() string { return d.name }

// Parent returns the parent directory, or nil for the root.
func (d *Dir) Parent() *Dir { return d.parent }

// Stats returns the maintained logical and physical byte counts of the
// subtree rooted at d.
func (d *Dir) Stats() (logical, physical int64) { return d.logical, d.physical }

// Children returns the sorted entry names of the directory.
func (d *Dir) Children() []string {
	names := make([]string, 0, len(d.children))
	for name := range d.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns the named child directory or file.
func (d *Dir) Lookup(name string) (*Dir, *File, error) {
	n, ok := d.children[name]
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	switch c := n.(type) {
	case *Dir:
		return c, nil, nil
	case *File:
		return nil, c, nil
	}
	return nil, nil, fmt.Errorf("%w: %q", ErrNotFound, name)
}

// Mkdir creates a child directory.
func (d *Dir) Mkdir(name string) (*Dir, error) {
	if !validName(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if _, ok := d.children[name]; ok {
		return nil, fmt.Errorf("%w: %q", ErrExists, name)
	}
	child := &Dir{store: d.store, children: make(map[string]node), refs: make(map[BlockID]int64)}
	d.attach(name, child)
	return child, nil
}

// AddFile creates a file entry with the given logical size and block
// references. Unknown blocks are registered; a size conflict with a known
// block fails the operation atomically.
func (d *Dir) AddFile(name string, size int64, blocks []Block) (*File, error) {
	if !validName(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: file %q has size %d", ErrNegativeSize, name, size)
	}
	if _, ok := d.children[name]; ok {
		return nil, fmt.Errorf("%w: %q", ErrExists, name)
	}
	if err := d.store.registerBlocks(blocks); err != nil {
		return nil, err
	}
	f := &File{store: d.store, size: size, blocks: append([]Block(nil), blocks...)}
	d.attach(name, f)
	return f, nil
}

// Link adds a hard-link entry for an existing file.
func (d *Dir) Link(name string, f *File) error {
	if !validName(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if f == nil || f.store != d.store {
		return fmt.Errorf("%w: file belongs to a different store", ErrCrossStore)
	}
	if _, ok := d.children[name]; ok {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	d.attach(name, f)
	return nil
}

// Move relocates the named entry into target under newName. Moving a
// directory into itself or one of its descendants is rejected. The
// operation validates fully before mutating, so a failed move changes
// nothing.
func (d *Dir) Move(name string, target *Dir, newName string) error {
	n, ok := d.children[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	if !validName(newName) {
		return fmt.Errorf("%w: %q", ErrInvalidName, newName)
	}
	if target == nil || target.store != d.store {
		return fmt.Errorf("%w: target directory", ErrCrossStore)
	}
	if d == target && name == newName {
		return nil
	}
	if _, taken := target.children[newName]; taken {
		return fmt.Errorf("%w: %q", ErrExists, newName)
	}
	if sub, isDir := n.(*Dir); isDir {
		for cur := target; cur != nil; cur = cur.parent {
			if cur == sub {
				return fmt.Errorf("%w: cannot move %q into %q", ErrCycle, name, newName)
			}
		}
	}
	n = d.detach(name)
	target.attach(newName, n)
	return nil
}

// Remove unlinks the named entry. Removing a directory drops its whole
// subtree; removing one hard link of a file keeps the file's other entries.
func (d *Dir) Remove(name string) error {
	if _, ok := d.children[name]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	d.detach(name)
	return nil
}

// SetBlocks replaces the file's logical size and block references,
// propagating the difference along the ancestors of every hard-link entry.
// Validation happens before any mutation, so a failure changes nothing.
func (f *File) SetBlocks(size int64, blocks []Block) error {
	if size < 0 {
		return fmt.Errorf("%w: file has size %d", ErrNegativeSize, size)
	}
	if err := f.store.registerBlocks(blocks); err != nil {
		return err
	}
	old := f.delta()
	f.size = size
	f.blocks = append([]Block(nil), blocks...)
	next := f.delta()
	for _, e := range f.entries {
		propagate(e.dir, old, -1)
		propagate(e.dir, next, +1)
	}
	return nil
}

// Size returns the file's logical size.
func (f *File) Size() int64 { return f.size }

// Blocks returns the file's block references.
func (f *File) Blocks() []Block { return append([]Block(nil), f.blocks...) }

// Entries returns the hard-link locations of the file as "dir/name" paths.
func (f *File) Entries() []string {
	paths := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		paths = append(paths, e.dir.Path()+"/"+e.name)
	}
	sort.Strings(paths)
	return paths
}

// Path returns the absolute path of the directory within the tree.
func (d *Dir) Path() string {
	var parts []string
	for cur := d; cur != nil && cur.parent != nil; cur = cur.parent {
		parts = append(parts, cur.name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

// Recompute derives the statistics of the subtree rooted at d from scratch
// by walking every entry. It serves as the reference implementation used
// to cross-check the incrementally maintained Stats.
func (d *Dir) Recompute() (logical, physical int64) {
	seen := make(map[BlockID]bool)
	var walk func(dir *Dir)
	walk = func(dir *Dir) {
		for _, n := range dir.children {
			switch c := n.(type) {
			case *Dir:
				walk(c)
			case *File:
				logical += c.size
				for _, b := range c.blocks {
					if !seen[b.ID] {
						seen[b.ID] = true
						physical += d.store.blocks[b.ID]
					}
				}
			}
		}
	}
	walk(d)
	return logical, physical
}

// Verify compares the maintained statistics of every directory in the
// subtree against a full recompute and returns the path of the first
// mismatch, or "" if everything is consistent.
func (d *Dir) Verify() string {
	logical, physical := d.Recompute()
	if logical != d.logical || physical != d.physical {
		return d.Path()
	}
	for _, n := range d.children {
		if sub, ok := n.(*Dir); ok {
			if bad := sub.Verify(); bad != "" {
				return bad
			}
		}
	}
	return ""
}
