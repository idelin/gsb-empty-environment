package manifest

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// VTree is an in-memory FS used to exercise plans without touching a real
// filesystem. The root ("/") is always a directory.
type VTree struct {
	root *vnode
}

type vnode struct {
	typ      EntryType
	data     []byte
	link     string
	children map[string]*vnode
}

// NewVTree returns an empty virtual tree.
func NewVTree() *VTree {
	return &VTree{root: &vnode{typ: Dir, children: map[string]*vnode{}}}
}

func split(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("vtree: path %q must be absolute", p)
	}
	c := path.Clean(p)
	if c == "/" {
		return nil, nil
	}
	return strings.Split(strings.TrimPrefix(c, "/"), "/"), nil
}

// lookup resolves p, returning the node or nil.
func (t *VTree) lookup(p string) *vnode {
	segs, err := split(p)
	if err != nil || segs == nil {
		return t.root
	}
	cur := t.root
	for _, s := range segs {
		if cur.typ != Dir {
			return nil
		}
		next, ok := cur.children[s]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// parent resolves the parent directory of p.
func (t *VTree) parent(p string) (*vnode, string, error) {
	segs, err := split(p)
	if err != nil {
		return nil, "", err
	}
	if segs == nil {
		return nil, "", fmt.Errorf("vtree: cannot use root here")
	}
	cur := t.root
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur.children[s]
		if cur.typ != Dir || !ok {
			return nil, "", fmt.Errorf("vtree: no such directory: %s", path.Dir(p))
		}
		cur = next
	}
	if cur.typ != Dir {
		return nil, "", fmt.Errorf("vtree: no such directory: %s", path.Dir(p))
	}
	return cur, segs[len(segs)-1], nil
}

// Stat implements FS.
func (t *VTree) Stat(p string) (Node, bool) {
	n := t.lookup(p)
	if n == nil {
		return Node{}, false
	}
	out := Node{Type: n.typ, Link: n.link}
	if n.typ == File {
		out.Digest = DigestBytes(n.data)
	}
	return out, true
}

// MkdirAll implements FS.
func (t *VTree) MkdirAll(p string) error {
	segs, err := split(p)
	if err != nil {
		return err
	}
	cur := t.root
	for _, s := range segs {
		next, ok := cur.children[s]
		if !ok {
			next = &vnode{typ: Dir, children: map[string]*vnode{}}
			cur.children[s] = next
		}
		if next.typ != Dir {
			return fmt.Errorf("vtree: %s: not a directory", p)
		}
		cur = next
	}
	return nil
}

// Write implements FS. It is atomic: the file appears fully formed.
func (t *VTree) Write(p string, data []byte) error {
	par, name, err := t.parent(p)
	if err != nil {
		return err
	}
	if n, ok := par.children[name]; ok && n.typ == Dir {
		return fmt.Errorf("vtree: %s: is a directory", p)
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	par.children[name] = &vnode{typ: File, data: cp}
	return nil
}

// Symlink implements FS.
func (t *VTree) Symlink(target, p string) error {
	par, name, err := t.parent(p)
	if err != nil {
		return err
	}
	if _, ok := par.children[name]; ok {
		return fmt.Errorf("vtree: %s: already exists", p)
	}
	par.children[name] = &vnode{typ: Symlink, link: target}
	return nil
}

// Rename implements FS.
func (t *VTree) Rename(src, dst string) error {
	srcPar, srcName, err := t.parent(src)
	if err != nil {
		return err
	}
	n, ok := srcPar.children[srcName]
	if !ok {
		return fmt.Errorf("vtree: %s: no such file", src)
	}
	dstPar, dstName, err := t.parent(dst)
	if err != nil {
		return err
	}
	if _, ok := dstPar.children[dstName]; ok {
		return fmt.Errorf("vtree: %s: already exists", dst)
	}
	delete(srcPar.children, srcName)
	dstPar.children[dstName] = n
	return nil
}

// Remove implements FS.
func (t *VTree) Remove(p string) error {
	par, name, err := t.parent(p)
	if err != nil {
		return err
	}
	n, ok := par.children[name]
	if !ok {
		return fmt.Errorf("vtree: %s: no such file", p)
	}
	if n.typ == Dir {
		return fmt.Errorf("vtree: %s: is a directory", p)
	}
	delete(par.children, name)
	return nil
}

// Rmdir implements FS.
func (t *VTree) Rmdir(p string) error {
	par, name, err := t.parent(p)
	if err != nil {
		return err
	}
	n, ok := par.children[name]
	if !ok {
		return fmt.Errorf("vtree: %s: no such directory", p)
	}
	if n.typ != Dir {
		return fmt.Errorf("vtree: %s: not a directory", p)
	}
	if len(n.children) > 0 {
		return fmt.Errorf("vtree: %s: directory not empty", p)
	}
	delete(par.children, name)
	return nil
}

// Children implements FS.
func (t *VTree) Children(p string) ([]string, bool) {
	n := t.lookup(p)
	if n == nil || n.typ != Dir {
		return nil, false
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

// Read returns the content of the file at p.
func (t *VTree) Read(p string) ([]byte, bool) {
	n := t.lookup(p)
	if n == nil || n.typ != File {
		return nil, false
	}
	cp := make([]byte, len(n.data))
	copy(cp, n.data)
	return cp, true
}
