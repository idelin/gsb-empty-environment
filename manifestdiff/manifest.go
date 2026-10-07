package manifestdiff

import (
	"fmt"
	"sort"
	"strings"
)

// Kind describes the type of a manifest entry.
type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindSymlink:
		return "symlink"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Entry is a single node in a manifest. Digest is meaningful for files,
// Target for symlinks; both are empty for directories.
type Entry struct {
	Path   string
	Kind   Kind
	Digest string
	Target string
}

// Manifest is a complete, normalized snapshot of a tree. Every non-root
// entry's ancestors are present as directories.
type Manifest struct {
	Entries map[string]Entry
}

// NormalizePath cleans a slash-separated relative path.
func NormalizePath(p string) (string, error) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid path %q", p)
		}
	}
	return p, nil
}

// Parent returns the parent path, or "" for top-level paths.
func Parent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func depth(p string) int {
	return strings.Count(p, "/") + 1
}

// NewManifest validates entries and builds a Manifest.
func NewManifest(entries []Entry) (*Manifest, error) {
	m := &Manifest{Entries: make(map[string]Entry, len(entries))}
	for _, e := range entries {
		p, err := NormalizePath(e.Path)
		if err != nil {
			return nil, err
		}
		e.Path = p
		if _, dup := m.Entries[p]; dup {
			return nil, fmt.Errorf("duplicate path %q", p)
		}
		switch e.Kind {
		case KindFile:
			if e.Digest == "" {
				return nil, fmt.Errorf("file %q has no digest", p)
			}
		case KindDir, KindSymlink:
		default:
			return nil, fmt.Errorf("path %q has unknown kind", p)
		}
		m.Entries[p] = e
	}
	for _, e := range m.Entries {
		parent := Parent(e.Path)
		if parent == "" {
			continue
		}
		pe, ok := m.Entries[parent]
		if !ok {
			return nil, fmt.Errorf("entry %q is missing ancestor directory %q", e.Path, parent)
		}
		if pe.Kind != KindDir {
			return nil, fmt.Errorf("ancestor %q of %q is not a directory", parent, e.Path)
		}
	}
	return m, nil
}

// Sorted returns entries ordered by path (parents before children).
func (m *Manifest) Sorted() []Entry {
	out := make([]Entry, 0, len(m.Entries))
	for _, e := range m.Entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Equal reports whether two manifests describe identical trees.
func (m *Manifest) Equal(other *Manifest) bool {
	if len(m.Entries) != len(other.Entries) {
		return false
	}
	for p, e := range m.Entries {
		oe, ok := other.Entries[p]
		if !ok || oe != e {
			return false
		}
	}
	return true
}
