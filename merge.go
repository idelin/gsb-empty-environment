// Package archivemerge merges generation-ordered manifests into per-snapshot
// visible trees. Each generation is a manifest whose entries are files,
// directories, or tombstones. A tombstone on a directory shadows that
// directory and all of its descendants carried over from older generations,
// but it does not shadow entries that the same (or a later) generation
// explicitly rebuilds beneath it.
package archivemerge

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies a manifest entry.
type Kind int

const (
	// File is a regular file entry.
	File Kind = iota
	// Dir is an explicit directory entry.
	Dir
	// Tombstone deletes the entry at its path and, for directories, every
	// descendant inherited from older generations.
	Tombstone
)

func (k Kind) String() string {
	switch k {
	case File:
		return "file"
	case Dir:
		return "dir"
	case Tombstone:
		return "tombstone"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Entry is a single manifest record. Path must be a slash-separated,
// cleaned relative path (e.g. "a/b/c"); "/" and "" are rejected.
type Entry struct {
	Path string
	Kind Kind
}

// Manifest is one generation of entries. Generations are merged in the
// order they are passed to Merge; Generation is an informational label
// recorded as the source of visible nodes.
type Manifest struct {
	Generation int
	Entries    []Entry
}

// record is the merged state for one live path.
type record struct {
	kind     Kind // File or Dir; never Tombstone
	source   int  // generation that supplied the entry
	explicit bool // false for directories implied by a descendant
}

// Node is one visible path in a snapshot tree.
type Node struct {
	Path     string
	Name     string
	Kind     Kind // File or Dir
	Source   int  // generation the entry came from
	Explicit bool // false for directories implied by a descendant
	Children []*Node
}

// Snapshot is the visible tree after applying one generation.
type Snapshot struct {
	Generation int
	Root       *Node
}

// Find returns the node at path, or nil if it is not visible.
func (s *Snapshot) Find(path string) *Node {
	path = strings.Trim(path, "/")
	if path == "" {
		return s.Root
	}
	node := s.Root
	for _, part := range strings.Split(path, "/") {
		var next *Node
		for _, c := range node.Children {
			if c.Name == part {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

// Flatten returns the visible nodes in sorted path order, excluding the
// synthetic root.
func (s *Snapshot) Flatten() []*Node {
	var out []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			out = append(out, c)
			walk(c)
		}
	}
	walk(s.Root)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Merge applies manifests in generation order and returns one snapshot per
// manifest, representing the visible tree after that generation.
//
// Within a generation, tombstones are applied before explicit entries so
// that a directory tombstone clears inherited descendants while explicit
// rebuild entries in the same generation still take effect.
func Merge(manifests []Manifest) ([]Snapshot, error) {
	state := map[string]record{}
	snapshots := make([]Snapshot, 0, len(manifests))
	for _, m := range manifests {
		if err := applyGeneration(state, m); err != nil {
			return nil, fmt.Errorf("generation %d: %v", m.Generation, err)
		}
		snapshots = append(snapshots, Snapshot{
			Generation: m.Generation,
			Root:       buildTree(state),
		})
	}
	return snapshots, nil
}

func applyGeneration(state map[string]record, m Manifest) error {
	for _, e := range m.Entries {
		if err := validatePath(e.Path); err != nil {
			return err
		}
	}
	// Phase 1: tombstones remove inherited state.
	for _, e := range m.Entries {
		if e.Kind == Tombstone {
			removeSubtree(state, e.Path)
		}
	}
	// Phase 2: explicit entries, parents first so deeper paths win.
	explicit := make([]Entry, 0, len(m.Entries))
	for _, e := range m.Entries {
		if e.Kind != Tombstone {
			explicit = append(explicit, e)
		}
	}
	sort.SliceStable(explicit, func(i, j int) bool {
		return depth(explicit[i].Path) < depth(explicit[j].Path)
	})
	for _, e := range explicit {
		putEntry(state, m.Generation, e)
	}
	return nil
}

// putEntry installs one file/dir entry, converting conflicting ancestors
// and descendants as needed.
func putEntry(state map[string]record, gen int, e Entry) {
	// Ancestors must be directories: a file ancestor is replaced by an
	// implicit directory (parent type change).
	parts := strings.Split(e.Path, "/")
	for i := 1; i < len(parts); i++ {
		ancestor := strings.Join(parts[:i], "/")
		rec, ok := state[ancestor]
		if !ok {
			state[ancestor] = record{kind: Dir, source: gen, explicit: false}
		} else if rec.kind != Dir {
			state[ancestor] = record{kind: Dir, source: gen, explicit: false}
		}
	}
	existing, ok := state[e.Path]
	if ok && existing.kind == Dir && e.Kind == File {
		// A file replaces a directory and its whole subtree.
		removeSubtree(state, e.Path)
	}
	if ok && existing.explicit && existing.kind == e.Kind {
		// Same-kind overwrite keeps the path but updates the source.
		state[e.Path] = record{kind: e.Kind, source: gen, explicit: true}
		return
	}
	if ok && existing.kind == Dir && e.Kind == Dir {
		// Implicit directory promoted to explicit; children survive.
		state[e.Path] = record{kind: Dir, source: gen, explicit: true}
		return
	}
	state[e.Path] = record{kind: e.Kind, source: gen, explicit: true}
}

// removeSubtree deletes path and every descendant from the state.
func removeSubtree(state map[string]record, path string) {
	prefix := path + "/"
	for p := range state {
		if p == path || strings.HasPrefix(p, prefix) {
			delete(state, p)
		}
	}
}

// buildTree materializes the flat state into a rooted tree. The root is a
// synthetic directory with Path "".
func buildTree(state map[string]record) *Node {
	root := &Node{Kind: Dir, Source: -1, Explicit: true}
	paths := make([]string, 0, len(state))
	for p := range state {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	nodes := map[string]*Node{"": root}
	for _, p := range paths {
		rec := state[p]
		parent := parentPath(p)
		pnode, ok := nodes[parent]
		if !ok {
			// Unreachable: putEntry always materializes ancestors.
			continue
		}
		n := &Node{
			Path:     p,
			Name:     p[strings.LastIndex(p, "/")+1:],
			Kind:     rec.kind,
			Source:   rec.source,
			Explicit: rec.explicit,
		}
		nodes[p] = n
		pnode.Children = append(pnode.Children, n)
	}
	return root
}

func parentPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func depth(p string) int { return strings.Count(p, "/") }

func validatePath(p string) error {
	if p == "" || p == "/" {
		return fmt.Errorf("invalid path %q", p)
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "//") {
		return fmt.Errorf("path %q must be a clean relative path", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("path %q contains %q segment", p, part)
		}
	}
	return nil
}
