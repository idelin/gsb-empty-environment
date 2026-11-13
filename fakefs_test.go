package netsec

import (
	"fmt"
	"io"
	"io/fs"
)

// fakeFS is a small in-memory filesystem used to inject races and hostile
// filesystem objects. Handles point at nodes, so deleting or replacing a path
// never changes what an existing handle sees.
type fakeFS struct {
	roots map[string]*fakeNode

	beforeOpenDir  func(dir *fakeDir, name string)
	beforeOpenFile func(dir *fakeDir, name string)

	nextIno  uint64
	nextTime int64
}

type fakeNode struct {
	mode       fs.FileMode
	data       []byte
	children   map[string]*fakeNode
	linkTarget *fakeNode

	device     uint64
	inode      uint64
	generation uint64
	links      uint64
	mtime      int64
	ctime      int64

	shortRead bool
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		roots:    make(map[string]*fakeNode),
		nextTime: 1,
	}
}

func (f *fakeFS) newInode(mode fs.FileMode) *fakeNode {
	f.nextIno++
	f.nextTime++
	return &fakeNode{
		mode:       mode,
		device:     1,
		inode:      f.nextIno,
		generation: 1,
		links:      1,
		mtime:      f.nextTime,
		ctime:      f.nextTime,
	}
}

func (f *fakeFS) addRoot(path string) *fakeNode {
	root := f.newInode(fs.ModeDir | 0o755)
	root.children = make(map[string]*fakeNode)
	f.roots[path] = root
	return root
}

func (f *fakeFS) addDir(parent *fakeNode, name string) *fakeNode {
	child := f.newInode(fs.ModeDir | 0o755)
	child.children = make(map[string]*fakeNode)
	parent.children[name] = child
	f.touch(parent)
	return child
}

func (f *fakeFS) addFile(parent *fakeNode, name string, data []byte) *fakeNode {
	child := f.newInode(0o644)
	child.data = append([]byte(nil), data...)
	parent.children[name] = child
	f.touch(parent)
	return child
}

func (f *fakeFS) addSymlink(parent *fakeNode, name string, target *fakeNode) *fakeNode {
	child := f.newInode(fs.ModeSymlink | 0o777)
	child.linkTarget = target
	parent.children[name] = child
	f.touch(parent)
	return child
}

func (f *fakeFS) addHardlink(parent *fakeNode, name string, target *fakeNode) *fakeNode {
	target.links++
	parent.children[name] = target
	f.touch(parent)
	f.nextTime++
	target.ctime = f.nextTime
	return target
}

func (f *fakeFS) remove(parent *fakeNode, name string) {
	delete(parent.children, name)
	f.touch(parent)
}

func (f *fakeFS) write(node *fakeNode, data []byte) {
	node.data = append([]byte(nil), data...)
	f.touch(node)
}

func (f *fakeFS) truncate(node *fakeNode, length int) {
	if length > len(node.data) {
		length = len(node.data)
	}
	node.data = node.data[:length]
	f.touch(node)
}

func (f *fakeFS) touch(node *fakeNode) {
	f.nextTime++
	node.mtime = f.nextTime
	node.ctime = f.nextTime
}

func (f *fakeFS) OpenModelDir(path string) (DirHandle, error) {
	root, ok := f.roots[path]
	if !ok {
		return nil, fmt.Errorf("fakefs: model root %q: %w", path, fs.ErrNotExist)
	}
	return &fakeDir{fs: f, node: root}, nil
}

type fakeDir struct {
	fs   *fakeFS
	node *fakeNode
}

func (d *fakeDir) Lstat(name string) (Entry, error) {
	child, ok := d.node.children[name]
	if !ok {
		return Entry{}, fmt.Errorf("fakefs: lstat %q: %w", name, fs.ErrNotExist)
	}
	return child.entry(), nil
}

func (d *fakeDir) OpenDir(name string) (DirHandle, error) {
	if d.fs.beforeOpenDir != nil {
		d.fs.beforeOpenDir(d, name)
	}
	child, ok := d.node.children[name]
	if !ok {
		return nil, fmt.Errorf("fakefs: open dir %q: %w", name, fs.ErrNotExist)
	}
	if child.mode&fs.ModeSymlink != 0 {
		child = child.linkTarget
		if child == nil {
			return nil, fmt.Errorf("fakefs: open dir %q: dangling symlink", name)
		}
	}
	if !child.mode.IsDir() {
		return nil, fmt.Errorf("fakefs: open dir %q: %w", name, fs.ErrInvalid)
	}
	return &fakeDir{fs: d.fs, node: child}, nil
}

func (d *fakeDir) OpenFile(name string) (FileHandle, error) {
	if d.fs.beforeOpenFile != nil {
		d.fs.beforeOpenFile(d, name)
	}
	child, ok := d.node.children[name]
	if !ok {
		return nil, fmt.Errorf("fakefs: open file %q: %w", name, fs.ErrNotExist)
	}
	if child.mode&fs.ModeSymlink != 0 {
		child = child.linkTarget
		if child == nil {
			return nil, fmt.Errorf("fakefs: open file %q: dangling symlink", name)
		}
	}
	return &fakeFile{node: child}, nil
}

func (d *fakeDir) Stat() (Entry, error) {
	return d.node.entry(), nil
}

func (d *fakeDir) Close() error {
	return nil
}

type fakeFile struct {
	node *fakeNode
}

func (f *fakeFile) ReadAt(p []byte, off int64) (int, error) {
	if f.node.shortRead {
		return 0, io.EOF
	}
	if off < 0 || off >= int64(len(f.node.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.node.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *fakeFile) Stat() (Entry, error) {
	return f.node.entry(), nil
}

func (f *fakeFile) Close() error {
	return nil
}

func (n *fakeNode) entry() Entry {
	size := int64(len(n.data))
	if n.mode.IsDir() || n.mode&fs.ModeSymlink != 0 {
		size = 0
	}
	identity := FileIdentity{
		Device:     n.device,
		Inode:      n.inode,
		Generation: n.generation,
		Links:      n.links,
		Size:       size,
		ModTime:    n.mtime,
		ChangeTime: n.ctime,
	}
	return Entry{Mode: n.mode, Size: size, Identity: identity}
}
