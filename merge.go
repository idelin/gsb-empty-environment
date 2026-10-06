// Package archivemerge merges generation-ordered archive manifests into
// per-snapshot visible trees with provenance.
//
// Model: generations are applied in order as an overlay fold over a single
// virtual tree. Each entry is a file, a directory, or a tombstone.
//
// Semantics:
//   - A file entry replaces whatever subtree existed at its path (parent
//     type change dir -> file hides the old descendants).
//   - A directory entry creates the directory if absent; if a file occupied
//     the path, the file is replaced (parent type change file -> dir).
//   - Implicit ancestor directories are created on demand; a file on the
//     ancestor path is replaced by an implicit directory.
//   - A file tombstone removes a visible file at its path only.
//   - A directory tombstone removes the whole visible subtree at its path,
//     hiding all older descendants. Entries from newer generations under
//     the same path are explicit rebuilds and remain visible.
//   - Every visible node records the generation that produced it.
package archivemerge

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Kind classifies a manifest entry or a visible node.
type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindFileTombstone
	KindDirTombstone
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindFileTombstone:
		return "file-tombstone"
	case KindDirTombstone:
		return "dir-tombstone"
	default:
		return "unknown"
	}
}

// Entry is a single manifest record.
type Entry struct {
	Path string
	Kind Kind
}

// Manifest is one generation of entries, applied atomically in list order.
type Manifest struct {
	Generation int
	Entries    []Entry
}

// Node is a visible path in a snapshot, with its provenance.
type Node struct {
	Path      string
	Kind      Kind // KindFile or KindDir only
	SourceGen int  // generation that created this node
	Implicit  bool // directory created as an ancestor of another entry
}

// Snapshot is the visible tree after applying one generation.
type Snapshot struct {
	Generation int
	Nodes      []Node // sorted by path
}

// Get returns the visible node at p, if any.
func (s Snapshot) Get(p string) (Node, bool) {
	p = normalize(p)
	for _, n := range s.Nodes {
		if n.Path == p {
			return n, true
		}
	}
	return Node{}, false
}

type treeNode struct {
	kind      Kind
	sourceGen int
	implicit  bool
	children  map[string]*treeNode
}

func newDir(gen int, implicit bool) *treeNode {
	return &treeNode{kind: KindDir, sourceGen: gen, implicit: implicit, children: map[string]*treeNode{}}
}

// Merger holds the folded virtual tree across generations.
type Merger struct {
	root *treeNode
}

// NewMerger returns an empty merger.
func NewMerger() *Merger {
	return &Merger{root: newDir(0, false)}
}

// Merge applies every manifest in order and returns the snapshot taken
// after each one.
func Merge(manifests []Manifest) ([]Snapshot, error) {
	m := NewMerger()
	snaps := make([]Snapshot, 0, len(manifests))
	for _, mf := range manifests {
		snap, err := m.Apply(mf)
		if err != nil {
			return nil, err
		}
		snaps = append(snaps, snap)
	}
	return snaps, nil
}

// Apply folds one generation into the tree and returns the resulting
// snapshot.
func (m *Merger) Apply(mf Manifest) (Snapshot, error) {
	for _, e := range mf.Entries {
		if err := m.applyEntry(mf.Generation, e); err != nil {
			return Snapshot{}, err
		}
	}
	return m.snapshot(mf.Generation), nil
}

func (m *Merger) applyEntry(gen int, e Entry) error {
	segs, err := splitPath(e.Path)
	if err != nil {
		return err
	}
	switch e.Kind {
	case KindFile:
		parent := m.ensureAncestors(segs, gen)
		parent.children[segs[len(segs)-1]] = &treeNode{kind: KindFile, sourceGen: gen}
	case KindDir:
		parent := m.ensureAncestors(segs, gen)
		name := segs[len(segs)-1]
		child, ok := parent.children[name]
		if !ok || child.kind != KindDir {
			parent.children[name] = newDir(gen, false)
		} else if child.implicit {
			child.implicit = false
			child.sourceGen = gen
		}
	case KindFileTombstone:
		if parent := m.walk(segs[:len(segs)-1]); parent != nil {
			if child, ok := parent.children[segs[len(segs)-1]]; ok && child.kind == KindFile {
				delete(parent.children, segs[len(segs)-1])
			}
		}
	case KindDirTombstone:
		if parent := m.walk(segs[:len(segs)-1]); parent != nil {
			if child, ok := parent.children[segs[len(segs)-1]]; ok && child.kind == KindDir {
				delete(parent.children, segs[len(segs)-1])
			}
		}
	default:
		return fmt.Errorf("unknown entry kind %d for %q", e.Kind, e.Path)
	}
	return nil
}

// ensureAncestors walks to the parent of segs, creating implicit
// directories as needed. A file found on the path is replaced by an
// implicit directory (parent type change file -> dir).
func (m *Merger) ensureAncestors(segs []string, gen int) *treeNode {
	cur := m.root
	for _, seg := range segs[:len(segs)-1] {
		child, ok := cur.children[seg]
		if !ok {
			child = newDir(gen, true)
			cur.children[seg] = child
		} else if child.kind != KindDir {
			child = newDir(gen, true)
			cur.children[seg] = child
		}
		cur = child
	}
	return cur
}

// walk descends existing directories only; returns nil if any segment is
// missing or not a directory.
func (m *Merger) walk(segs []string) *treeNode {
	cur := m.root
	for _, seg := range segs {
		child, ok := cur.children[seg]
		if !ok || child.kind != KindDir {
			return nil
		}
		cur = child
	}
	return cur
}

func (m *Merger) snapshot(gen int) Snapshot {
	var nodes []Node
	var walk func(n *treeNode, prefix string)
	walk = func(n *treeNode, prefix string) {
		names := make([]string, 0, len(n.children))
		for name := range n.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := n.children[name]
			p := prefix + "/" + name
			nodes = append(nodes, Node{
				Path:      p,
				Kind:      child.kind,
				SourceGen: child.sourceGen,
				Implicit:  child.implicit,
			})
			if child.kind == KindDir {
				walk(child, p)
			}
		}
	}
	walk(m.root, "")
	return Snapshot{Generation: gen, Nodes: nodes}
}

func normalize(p string) string {
	return path.Clean("/" + p)
}

func splitPath(p string) ([]string, error) {
	clean := normalize(p)
	if clean == "/" {
		return nil, fmt.Errorf("invalid entry path %q", p)
	}
	return strings.Split(strings.TrimPrefix(clean, "/"), "/"), nil
}
