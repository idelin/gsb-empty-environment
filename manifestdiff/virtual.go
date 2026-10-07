package manifestdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// DigestOf computes the content digest used by manifests and the
// VirtualTree.
func DigestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

type node struct {
	kind     Kind
	content  []byte
	target   string
	children map[string]*node
}

// VirtualTree is an in-memory Executor used to verify plans.
type VirtualTree struct {
	root *node
}

func NewVirtualTree() *VirtualTree {
	return &VirtualTree{root: &node{kind: KindDir, children: map[string]*node{}}}
}

func splitPath(p string) ([]string, error) {
	np, err := NormalizePath(p)
	if err != nil {
		return nil, err
	}
	return strings.Split(np, "/"), nil
}

func (t *VirtualTree) lookup(p string) (*node, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, err
	}
	cur := t.root
	for _, part := range parts {
		if cur.kind != KindDir {
			return nil, nil
		}
		next, ok := cur.children[part]
		if !ok {
			return nil, nil
		}
		cur = next
	}
	return cur, nil
}

// parentOf resolves the parent directory node and base name for p.
func (t *VirtualTree) parentOf(p string) (*node, string, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, "", err
	}
	cur := t.root
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur.children[part]
		if !ok || next.kind != KindDir {
			return nil, "", fmt.Errorf("no parent directory for %q", p)
		}
		cur = next
	}
	return cur, parts[len(parts)-1], nil
}

func (t *VirtualTree) Stat(p string) (Entry, bool) {
	n, err := t.lookup(p)
	if err != nil || n == nil {
		return Entry{}, false
	}
	e := Entry{Path: p, Kind: n.kind}
	switch n.kind {
	case KindFile:
		e.Digest = DigestOf(n.content)
	case KindSymlink:
		e.Target = n.target
	}
	return e, true
}

func (t *VirtualTree) Mkdir(p string) error {
	parent, base, err := t.parentOf(p)
	if err != nil {
		return err
	}
	if _, exists := parent.children[base]; exists {
		return fmt.Errorf("mkdir %q: already exists", p)
	}
	parent.children[base] = &node{kind: KindDir, children: map[string]*node{}}
	return nil
}

func (t *VirtualTree) WriteFile(p string, content []byte) error {
	parent, base, err := t.parentOf(p)
	if err != nil {
		return err
	}
	if n, exists := parent.children[base]; exists {
		if n.kind != KindFile {
			return fmt.Errorf("write %q: existing node is a %s", p, n.kind)
		}
		n.content = append([]byte(nil), content...)
		return nil
	}
	parent.children[base] = &node{kind: KindFile, content: append([]byte(nil), content...)}
	return nil
}

func (t *VirtualTree) Symlink(p, target string) error {
	parent, base, err := t.parentOf(p)
	if err != nil {
		return err
	}
	if _, exists := parent.children[base]; exists {
		return fmt.Errorf("symlink %q: already exists", p)
	}
	parent.children[base] = &node{kind: KindSymlink, target: target}
	return nil
}

func (t *VirtualTree) Remove(p string) error {
	parent, base, err := t.parentOf(p)
	if err != nil {
		return err
	}
	n, exists := parent.children[base]
	if !exists {
		return fmt.Errorf("remove %q: does not exist", p)
	}
	if n.kind == KindDir && len(n.children) > 0 {
		return fmt.Errorf("remove %q: directory not empty", p)
	}
	delete(parent.children, base)
	return nil
}

func (t *VirtualTree) Rename(src, dst string) error {
	srcParent, srcBase, err := t.parentOf(src)
	if err != nil {
		return err
	}
	n, exists := srcParent.children[srcBase]
	if !exists {
		return fmt.Errorf("rename %q: does not exist", src)
	}
	dstParent, dstBase, err := t.parentOf(dst)
	if err != nil {
		return err
	}
	if _, exists := dstParent.children[dstBase]; exists {
		return fmt.Errorf("rename: destination %q already exists", dst)
	}
	delete(srcParent.children, srcBase)
	dstParent.children[dstBase] = n
	return nil
}

// Snapshot returns the complete manifest of the current tree state.
func (t *VirtualTree) Snapshot() *Manifest {
	var entries []Entry
	var walk func(n *node, prefix string)
	walk = func(n *node, prefix string) {
		for name, c := range n.children {
			p := name
			if prefix != "" {
				p = prefix + "/" + name
			}
			e := Entry{Path: p, Kind: c.kind}
			switch c.kind {
			case KindFile:
				e.Digest = DigestOf(c.content)
			case KindSymlink:
				e.Target = c.target
			}
			entries = append(entries, e)
			if c.kind == KindDir {
				walk(c, p)
			}
		}
	}
	walk(t.root, "")
	m, err := NewManifest(entries)
	if err != nil {
		panic(err)
	}
	return m
}

// Clone deep-copies the tree.
func (t *VirtualTree) Clone() *VirtualTree {
	var cp func(n *node) *node
	cp = func(n *node) *node {
		out := &node{kind: n.kind, content: append([]byte(nil), n.content...), target: n.target}
		if n.children != nil {
			out.children = make(map[string]*node, len(n.children))
			for name, c := range n.children {
				out.children[name] = cp(c)
			}
		}
		return out
	}
	return &VirtualTree{root: cp(t.root)}
}

// BuildTree materializes a manifest into a VirtualTree, fetching file
// content from store.
func BuildTree(m *Manifest, store ContentStore) (*VirtualTree, error) {
	t := NewVirtualTree()
	for _, e := range m.Sorted() {
		var err error
		switch e.Kind {
		case KindDir:
			err = t.Mkdir(e.Path)
		case KindFile:
			var content []byte
			if content, err = store.Get(e.Digest); err == nil {
				err = t.WriteFile(e.Path, content)
			}
		case KindSymlink:
			err = t.Symlink(e.Path, e.Target)
		}
		if err != nil {
			return nil, fmt.Errorf("build %q: %w", e.Path, err)
		}
	}
	return t, nil
}
