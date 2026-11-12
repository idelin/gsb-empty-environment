// Package fakefs provides a schedulable in-memory stand-in for the
// netsec.Dir capability. Tests use it to rehearse attacker
// interleavings — entry replacement, symlink loops, injected
// failures — at exact points of a reclamation round, and to prove
// that objects outside the reclaimed root are never touched.
package fakefs

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	netsec "example.org/netsec/q0263"
)

// maxResolve bounds symlink resolution by the harness itself.
const maxResolve = 40

type node struct {
	typ      netsec.FileType
	target   string // symlink target, an absolute fake-world path
	children map[string]*node
	ino      uint64
}

// Op identifies a capability operation for hook scheduling.
type Op int

const (
	OpReadDir Op = iota
	OpLstat
	OpOpenDir
	OpStat
	OpUnlink
	OpRmdir
)

func (op Op) String() string {
	switch op {
	case OpReadDir:
		return "readdir"
	case OpLstat:
		return "lstat"
	case OpOpenDir:
		return "opendir"
	case OpStat:
		return "stat"
	case OpUnlink:
		return "unlink"
	case OpRmdir:
		return "rmdir"
	default:
		return "unknown"
	}
}

// OpRecord is one attempted capability operation, in order of issue.
type OpRecord struct {
	Op   Op
	Path string
}

// Hook schedules Fn to run before matching capability operations,
// emulating an attacker racing the traversal. Path restricts the
// match; the empty Path matches every path. N > 0 fires only on the
// Nth match, N == 0 fires on every match. If Fn returns an error the
// operation fails with that error and has no effect.
type Hook struct {
	Op   Op
	Path string
	N    int
	Fn   func(f *FS) error

	seen int
}

// FS is one fake filesystem world. Capability operations can only
// reach objects below the directory they were opened on; the harness
// methods (MkdirAll, WriteFile, Symlink, RemoveAll, ...) address the
// whole world by absolute path and play the attacker.
type FS struct {
	root    *node
	nextIno uint64
	hooks   []*Hook

	// Ops logs every attempted capability operation.
	Ops []OpRecord
}

// New returns an empty world containing only the root directory "/".
func New() *FS {
	f := &FS{nextIno: 1}
	f.root = &node{typ: netsec.TypeDir, children: map[string]*node{}, ino: f.alloc()}
	return f
}

func (f *FS) alloc() uint64 {
	ino := f.nextIno
	f.nextIno++
	return ino
}

// AddHook schedules h.
func (f *FS) AddHook(h *Hook) { f.hooks = append(f.hooks, h) }

func (f *FS) runHooks(op Op, path string) error {
	for _, h := range f.hooks {
		if h.Op != op || (h.Path != "" && h.Path != path) {
			continue
		}
		h.seen++
		if h.N > 0 && h.seen != h.N {
			continue
		}
		if h.Fn != nil {
			if err := h.Fn(f); err != nil {
				return err
			}
		}
	}
	return nil
}

// MkdirAll creates the directory at path and any missing parents.
func (f *FS) MkdirAll(p string) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}
	cur := f.root
	for _, name := range parts {
		next, ok := cur.children[name]
		if !ok {
			next = &node{typ: netsec.TypeDir, children: map[string]*node{}, ino: f.alloc()}
			cur.children[name] = next
		} else if next.typ != netsec.TypeDir {
			return fmt.Errorf("mkdir %s: not a directory", p)
		}
		cur = next
	}
	return nil
}

// WriteFile creates or replaces the regular file at path. Replacing
// allocates a fresh identity, like a real rename would.
func (f *FS) WriteFile(p string) error {
	parent, base, err := f.parent(p)
	if err != nil {
		return err
	}
	parent.children[base] = &node{typ: netsec.TypeFile, ino: f.alloc()}
	return nil
}

// Symlink creates a symlink at linkPath pointing to the absolute
// (fake-world) target. Capability operations never resolve it.
func (f *FS) Symlink(target, linkPath string) error {
	parent, base, err := f.parent(linkPath)
	if err != nil {
		return err
	}
	parent.children[base] = &node{typ: netsec.TypeSymlink, target: target, ino: f.alloc()}
	return nil
}

// RemoveAll deletes the object at path, whatever its type.
func (f *FS) RemoveAll(p string) error {
	parent, base, err := f.parent(p)
	if err != nil {
		return err
	}
	delete(parent.children, base)
	return nil
}

// Exists reports whether path resolves, not following a final symlink.
func (f *FS) Exists(p string) bool {
	_, err := f.resolve(p, false)
	return err == nil
}

// Lstat reports the object at path without following a final symlink.
func (f *FS) Lstat(p string) (netsec.Info, error) {
	n, err := f.resolve(p, false)
	if err != nil {
		return netsec.Info{}, err
	}
	return netsec.Info{ID: netsec.Identity{Dev: 1, Ino: n.ino}, Type: n.typ}, nil
}

// Entries lists the sorted entry names of the directory at path.
func (f *FS) Entries(p string) ([]string, error) {
	n, err := f.resolve(p, true)
	if err != nil {
		return nil, err
	}
	if n.typ != netsec.TypeDir {
		return nil, fmt.Errorf("entries %s: not a directory", p)
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Open returns a capability to the directory at path. Unlike
// capability operations, Open resolves symlinks in path: it models
// the cleanup host opening the temporary directory it was handed.
func (f *FS) Open(p string) (netsec.Dir, error) {
	n, err := f.resolve(p, true)
	if err != nil {
		return nil, err
	}
	if n.typ != netsec.TypeDir {
		return nil, fmt.Errorf("open %s: not a directory", p)
	}
	return &dir{fs: f, node: n, path: path.Clean(p)}, nil
}

func splitPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("path %q: must be absolute", p)
	}
	clean := path.Clean(p)
	if clean == "/" {
		return nil, nil
	}
	return strings.Split(strings.TrimPrefix(clean, "/"), "/"), nil
}

func (f *FS) parent(p string) (*node, string, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, "", err
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("path %q: has no parent", p)
	}
	n, err := f.resolveParts(parts[:len(parts)-1], true)
	if err != nil {
		return nil, "", err
	}
	if n.typ != netsec.TypeDir {
		return nil, "", fmt.Errorf("path %q: parent is not a directory", p)
	}
	return n, parts[len(parts)-1], nil
}

func (f *FS) resolve(p string, followFinal bool) (*node, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, err
	}
	return f.resolveParts(parts, followFinal)
}

func (f *FS) resolveParts(parts []string, followFinal bool) (*node, error) {
	cur := f.root
	resolves := 0
	i := 0
	for i < len(parts) {
		if cur.typ != netsec.TypeDir {
			return nil, fmt.Errorf("resolve /%s: not a directory", strings.Join(parts, "/"))
		}
		next, ok := cur.children[parts[i]]
		if !ok {
			return nil, fmt.Errorf("resolve /%s: %w", strings.Join(parts, "/"), fs.ErrNotExist)
		}
		final := i == len(parts)-1
		if next.typ == netsec.TypeSymlink && (!final || followFinal) {
			resolves++
			if resolves > maxResolve {
				return nil, fmt.Errorf("resolve /%s: too many levels of symbolic links", strings.Join(parts, "/"))
			}
			rest := append([]string{}, parts[i+1:]...)
			targetParts, err := splitPath(next.target)
			if err != nil {
				return nil, err
			}
			parts = append(targetParts, rest...)
			cur = f.root
			i = 0
			continue
		}
		cur = next
		i++
	}
	return cur, nil
}

// dir is a capability to one opened directory node. The node pointer
// pins the exact generation that was confirmed when the capability
// was opened, even if the name is replaced afterwards.
type dir struct {
	fs     *FS
	node   *node
	path   string
	closed bool
}

var _ netsec.Dir = (*dir)(nil)

func (d *dir) step(op Op, name string) (string, error) {
	if d.closed {
		return "", fmt.Errorf("%s %s: %w", op, d.path, fs.ErrClosed)
	}
	p := d.path
	if name != "" {
		if !validName(name) {
			return "", fmt.Errorf("%s %q: %w", op, name, fs.ErrInvalid)
		}
		p = joinPath(d.path, name)
	}
	d.fs.Ops = append(d.fs.Ops, OpRecord{Op: op, Path: p})
	if err := d.fs.runHooks(op, p); err != nil {
		return p, err
	}
	return p, nil
}

func (d *dir) ReadDir() ([]netsec.Entry, error) {
	if _, err := d.step(OpReadDir, ""); err != nil {
		return nil, err
	}
	entries := make([]netsec.Entry, 0, len(d.node.children))
	for name, child := range d.node.children {
		entries = append(entries, netsec.Entry{Name: name, Type: child.typ})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (d *dir) Lstat(name string) (netsec.Info, error) {
	p, err := d.step(OpLstat, name)
	if err != nil {
		return netsec.Info{}, err
	}
	child, ok := d.node.children[name]
	if !ok {
		return netsec.Info{}, fmt.Errorf("lstat %s: %w", p, fs.ErrNotExist)
	}
	return netsec.Info{ID: netsec.Identity{Dev: 1, Ino: child.ino}, Type: child.typ}, nil
}

func (d *dir) OpenDir(name string) (netsec.Dir, error) {
	p, err := d.step(OpOpenDir, name)
	if err != nil {
		return nil, err
	}
	child, ok := d.node.children[name]
	if !ok {
		return nil, fmt.Errorf("opendir %s: %w", p, fs.ErrNotExist)
	}
	switch child.typ {
	case netsec.TypeSymlink:
		return nil, fmt.Errorf("opendir %s: too many levels of symbolic links", p)
	case netsec.TypeDir:
		return &dir{fs: d.fs, node: child, path: p}, nil
	default:
		return nil, fmt.Errorf("opendir %s: not a directory", p)
	}
}

func (d *dir) Stat() (netsec.Info, error) {
	if _, err := d.step(OpStat, ""); err != nil {
		return netsec.Info{}, err
	}
	return netsec.Info{ID: netsec.Identity{Dev: 1, Ino: d.node.ino}, Type: d.node.typ}, nil
}

func (d *dir) Unlink(name string) error {
	p, err := d.step(OpUnlink, name)
	if err != nil {
		return err
	}
	child, ok := d.node.children[name]
	if !ok {
		return fmt.Errorf("unlink %s: %w", p, fs.ErrNotExist)
	}
	if child.typ == netsec.TypeDir {
		return fmt.Errorf("unlink %s: is a directory", p)
	}
	delete(d.node.children, name)
	return nil
}

func (d *dir) Rmdir(name string) error {
	p, err := d.step(OpRmdir, name)
	if err != nil {
		return err
	}
	child, ok := d.node.children[name]
	if !ok {
		return fmt.Errorf("rmdir %s: %w", p, fs.ErrNotExist)
	}
	if child.typ != netsec.TypeDir {
		return fmt.Errorf("rmdir %s: not a directory", p)
	}
	if len(child.children) > 0 {
		return fmt.Errorf("rmdir %s: directory not empty", p)
	}
	delete(d.node.children, name)
	return nil
}

func (d *dir) Close() error {
	d.closed = true
	return nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

func joinPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}
