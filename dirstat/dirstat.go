// Package dirstat maintains per-directory capacity statistics for a
// directory tree containing files, hard links and shared content blocks.
//
// Two metrics are maintained for every directory subtree:
//
//   - LogicalBytes: sum of file sizes, counting every directory entry
//     (hard links are counted once per link).
//   - PhysicalBytes: sum of the sizes of the distinct blocks referenced
//     by the subtree. A block shared by several files or links is only
//     counted once per directory subtree.
//
// Statistics are updated incrementally along the ancestor chain of every
// mutation. All mutating operations validate completely before touching
// any state, so a failed operation never leaves partial additions or
// subtractions behind. Moves that would create a directory cycle are
// rejected.
package dirstat

import (
	"errors"
	"fmt"
	"strings"
)

// BlockID identifies a content block in the block store.
type BlockID string

// Stats is a point-in-time capacity snapshot of a directory subtree.
type Stats struct {
	LogicalBytes  int64
	PhysicalBytes int64
}

var (
	ErrNotFound     = errors.New("dirstat: path not found")
	ErrNotDir       = errors.New("dirstat: not a directory")
	ErrNotFile      = errors.New("dirstat: not a regular file")
	ErrExists       = errors.New("dirstat: entry already exists")
	ErrCycle        = errors.New("dirstat: move would create a directory cycle")
	ErrUnknownBlock = errors.New("dirstat: unknown block")
	ErrInvalidSize  = errors.New("dirstat: negative size")
	ErrRootOp       = errors.New("dirstat: operation not allowed on the root")
)

type node interface{ isNode() }

// File is a regular file. A single File may be linked from several
// directories (hard links); links records every directory holding one.
type File struct {
	size   int64
	blocks []BlockID
	links  []*Dir
}

func (*File) isNode() {}

// Dir is a directory. It maintains aggregate statistics for its subtree:
// logical is the summed logical bytes, refs counts block references inside
// the subtree and physical is the summed size of blocks with refs > 0.
type Dir struct {
	parent   *Dir
	name     string
	children map[string]node
	logical  int64
	physical int64
	refs     map[BlockID]int
}

func (*Dir) isNode() {}

// FS is a directory tree plus the block store it references.
type FS struct {
	root   *Dir
	blocks map[BlockID]int64
}

// New returns an empty file system with a single root directory "/".
func New() *FS {
	return &FS{
		root:   newDir(nil, ""),
		blocks: make(map[BlockID]int64),
	}
}

func newDir(parent *Dir, name string) *Dir {
	return &Dir{
		parent:   parent,
		name:     name,
		children: make(map[string]node),
		refs:     make(map[BlockID]int),
	}
}

// RegisterBlock adds a block of the given size to the block store.
// Registering an existing block with the same size is a no-op; a
// conflicting size is an error.
func (fs *FS) RegisterBlock(id BlockID, size int64) error {
	if size < 0 {
		return ErrInvalidSize
	}
	if existing, ok := fs.blocks[id]; ok {
		if existing != size {
			return fmt.Errorf("dirstat: block %q already registered with size %d", id, existing)
		}
		return nil
	}
	fs.blocks[id] = size
	return nil
}

// splitPath splits "/a/b/c" into ["a","b","c"]; "/" yields nil.
func splitPath(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("dirstat: path %q is not absolute", path)
	}
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, fmt.Errorf("dirstat: invalid path %q", path)
		}
	}
	return parts, nil
}

// resolve returns the node at path, or nil, ErrNotFound.
func (fs *FS) resolve(path string) (node, error) {
	parts, err := splitPath(path)
	if err != nil {
		return nil, err
	}
	cur := node(fs.root)
	for _, p := range parts {
		d, ok := cur.(*Dir)
		if !ok {
			return nil, ErrNotFound
		}
		child, ok := d.children[p]
		if !ok {
			return nil, ErrNotFound
		}
		cur = child
	}
	return cur, nil
}

// resolveParent resolves all but the last path component to a directory
// and returns it together with the final component.
func (fs *FS) resolveParent(path string) (*Dir, string, error) {
	parts, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	if len(parts) == 0 {
		return nil, "", ErrRootOp
	}
	parentPath := "/" + strings.Join(parts[:len(parts)-1], "/")
	n, err := fs.resolve(parentPath)
	if err != nil {
		return nil, "", err
	}
	d, ok := n.(*Dir)
	if !ok {
		return nil, "", ErrNotDir
	}
	return d, parts[len(parts)-1], nil
}

// addDelta adds (logical, blocks) to dir and every ancestor.
func (fs *FS) addDelta(dir *Dir, logical int64, blocks []BlockID) {
	for d := dir; d != nil; d = d.parent {
		d.logical += logical
		for _, b := range blocks {
			if d.refs[b] == 0 {
				d.physical += fs.blocks[b]
			}
			d.refs[b]++
		}
	}
}

// removeDelta subtracts (logical, blocks) from dir and every ancestor.
func (fs *FS) removeDelta(dir *Dir, logical int64, blocks []BlockID) {
	for d := dir; d != nil; d = d.parent {
		d.logical -= logical
		for _, b := range blocks {
			d.refs[b]--
			if d.refs[b] == 0 {
				d.physical -= fs.blocks[b]
				delete(d.refs, b)
			}
		}
	}
}

// addRefsDelta is addDelta for a subtree described by a refcount map.
func (fs *FS) addRefsDelta(dir *Dir, logical int64, refs map[BlockID]int) {
	for d := dir; d != nil; d = d.parent {
		d.logical += logical
		for b, n := range refs {
			if d.refs[b] == 0 {
				d.physical += fs.blocks[b]
			}
			d.refs[b] += n
		}
	}
}

// removeRefsDelta is removeDelta for a subtree described by a refcount map.
func (fs *FS) removeRefsDelta(dir *Dir, logical int64, refs map[BlockID]int) {
	for d := dir; d != nil; d = d.parent {
		d.logical -= logical
		for b, n := range refs {
			d.refs[b] -= n
			if d.refs[b] == 0 {
				d.physical -= fs.blocks[b]
				delete(d.refs, b)
			}
		}
	}
}

// subtreeRefs returns the aggregate (logical bytes, block refcounts) of n.
func subtreeRefs(n node) (int64, map[BlockID]int) {
	switch t := n.(type) {
	case *File:
		refs := make(map[BlockID]int, len(t.blocks))
		for _, b := range t.blocks {
			refs[b]++
		}
		return t.size, refs
	case *Dir:
		return t.logical, t.refs
	}
	panic("dirstat: unknown node type")
}

// Mkdir creates a directory at path. The parent must exist.
func (fs *FS) Mkdir(path string) error {
	parent, name, err := fs.resolveParent(path)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return ErrExists
	}
	parent.children[name] = newDir(parent, name)
	return nil
}

// AddFile creates a regular file at path referencing the given blocks.
// All blocks must be registered. The file is created with one link.
func (fs *FS) AddFile(path string, size int64, blocks []BlockID) error {
	if size < 0 {
		return ErrInvalidSize
	}
	parent, name, err := fs.resolveParent(path)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return ErrExists
	}
	for _, b := range blocks {
		if _, ok := fs.blocks[b]; !ok {
			return fmt.Errorf("%s: %q", ErrUnknownBlock, b)
		}
	}
	f := &File{size: size, blocks: append([]BlockID(nil), blocks...), links: []*Dir{parent}}
	parent.children[name] = f
	fs.addDelta(parent, size, blocks)
	return nil
}

// Link creates an additional hard link at linkPath to the file at filePath.
func (fs *FS) Link(filePath, linkPath string) error {
	n, err := fs.resolve(filePath)
	if err != nil {
		return err
	}
	f, ok := n.(*File)
	if !ok {
		return ErrNotFile
	}
	parent, name, err := fs.resolveParent(linkPath)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return ErrExists
	}
	parent.children[name] = f
	f.links = append(f.links, parent)
	fs.addDelta(parent, f.size, f.blocks)
	return nil
}

// Unlink removes the entry at path. Directories are removed together with
// their subtree. Removing the last link to a file releases its blocks.
func (fs *FS) Unlink(path string) error {
	parent, name, err := fs.resolveParent(path)
	if err != nil {
		return err
	}
	n, ok := parent.children[name]
	if !ok {
		return ErrNotFound
	}
	logical, refs := subtreeRefs(n)
	delete(parent.children, name)
	switch t := n.(type) {
	case *File:
		t.removeLink(parent)
	case *Dir:
		detachDir(t)
		t.parent = nil
	}
	fs.removeRefsDelta(parent, logical, refs)
	return nil
}

// detachDir removes d (and its subdirectories) from the link lists of
// every file in its subtree, so later updates to those files cannot
// propagate into the detached tree.
func detachDir(d *Dir) {
	for _, child := range d.children {
		switch t := child.(type) {
		case *File:
			t.removeLink(d)
		case *Dir:
			detachDir(t)
		}
	}
}

// Move relocates the entry at srcPath to dstPath (which must not exist).
// It handles renames within the same parent as well as subtree moves
// across directories. Moves that would make a directory its own ancestor
// are rejected with ErrCycle.
func (fs *FS) Move(srcPath, dstPath string) error {
	srcParent, srcName, err := fs.resolveParent(srcPath)
	if err != nil {
		return err
	}
	n, ok := srcParent.children[srcName]
	if !ok {
		return ErrNotFound
	}
	dstParent, dstName, err := fs.resolveParent(dstPath)
	if err != nil {
		return err
	}
	if _, ok := dstParent.children[dstName]; ok {
		return ErrExists
	}
	if d, isDir := n.(*Dir); isDir {
		for p := dstParent; p != nil; p = p.parent {
			if p == d {
				return ErrCycle
			}
		}
	}
	// All validation is done; mutations below cannot fail.
	logical, refs := subtreeRefs(n)
	fs.removeRefsDelta(srcParent, logical, refs)
	delete(srcParent.children, srcName)
	dstParent.children[dstName] = n
	fs.addRefsDelta(dstParent, logical, refs)
	switch t := n.(type) {
	case *Dir:
		t.parent = dstParent
		t.name = dstName
	case *File:
		t.removeLink(srcParent)
		t.links = append(t.links, dstParent)
	}
	return nil
}

// ReplaceBlocks atomically swaps the size and block references of the
// file at path. All ancestors of every hard link are updated. If any new
// block is unknown, nothing is changed.
func (fs *FS) ReplaceBlocks(path string, newSize int64, newBlocks []BlockID) error {
	if newSize < 0 {
		return ErrInvalidSize
	}
	n, err := fs.resolve(path)
	if err != nil {
		return err
	}
	f, ok := n.(*File)
	if !ok {
		return ErrNotFile
	}
	for _, b := range newBlocks {
		if _, ok := fs.blocks[b]; !ok {
			return fmt.Errorf("%s: %q", ErrUnknownBlock, b)
		}
	}
	// Validated; apply. Update every link's ancestor chain.
	oldSize, oldBlocks := f.size, f.blocks
	for _, linkDir := range f.links {
		fs.removeDelta(linkDir, oldSize, oldBlocks)
	}
	f.size = newSize
	f.blocks = append([]BlockID(nil), newBlocks...)
	for _, linkDir := range f.links {
		fs.addDelta(linkDir, newSize, newBlocks)
	}
	return nil
}

func (f *File) removeLink(d *Dir) {
	for i, link := range f.links {
		if link == d {
			f.links = append(f.links[:i], f.links[i+1:]...)
			return
		}
	}
}

// Stats returns the maintained statistics for the subtree at path.
func (fs *FS) Stats(path string) (Stats, error) {
	n, err := fs.resolve(path)
	if err != nil {
		return Stats{}, err
	}
	switch t := n.(type) {
	case *Dir:
		return Stats{LogicalBytes: t.logical, PhysicalBytes: t.physical}, nil
	case *File:
		var physical int64
		seen := make(map[BlockID]bool, len(t.blocks))
		for _, b := range t.blocks {
			if !seen[b] {
				seen[b] = true
				physical += fs.blocks[b]
			}
		}
		return Stats{LogicalBytes: t.size, PhysicalBytes: physical}, nil
	}
	panic("dirstat: unknown node type")
}

// Recompute walks the subtree at path from scratch and returns its exact
// statistics. It is the reference implementation used to cross-check the
// incrementally maintained values returned by Stats.
func (fs *FS) Recompute(path string) (Stats, error) {
	n, err := fs.resolve(path)
	if err != nil {
		return Stats{}, err
	}
	logical, refs := fs.walk(n)
	var physical int64
	for b := range refs {
		physical += fs.blocks[b]
	}
	return Stats{LogicalBytes: logical, PhysicalBytes: physical}, nil
}

func (fs *FS) walk(n node) (int64, map[BlockID]int) {
	refs := make(map[BlockID]int)
	switch t := n.(type) {
	case *File:
		for _, b := range t.blocks {
			refs[b]++
		}
		return t.size, refs
	case *Dir:
		var logical int64
		for _, child := range t.children {
			childLogical, childRefs := fs.walk(child)
			logical += childLogical
			for b, count := range childRefs {
				refs[b] += count
			}
		}
		return logical, refs
	}
	panic("dirstat: unknown node type")
}
