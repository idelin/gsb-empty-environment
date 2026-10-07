// Package manifest compares two file-tree manifests and produces an
// idempotent, replayable update plan. A virtual-tree executor applies the
// plan and verifies the result matches the target exactly.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
)

// EntryType classifies a manifest entry.
type EntryType int

const (
	File EntryType = iota
	Dir
	Symlink
)

func (t EntryType) String() string {
	switch t {
	case File:
		return "file"
	case Dir:
		return "dir"
	case Symlink:
		return "symlink"
	}
	return "unknown"
}

// Entry describes one node of a tree. Digest is set for files (sha256 of
// content), Link is set for symlinks (the link target).
type Entry struct {
	Type   EntryType
	Digest string
	Link   string
}

// Manifest maps slash-separated, root-relative paths to entries. Paths are
// normalized with cleanPath; ancestors of listed paths are implicitly
// directories even when not listed.
type Manifest map[string]Entry

// DigestBytes returns the content digest used throughout the package.
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileEntry builds a file entry from content.
func FileEntry(data []byte) Entry { return Entry{Type: File, Digest: DigestBytes(data)} }

// DirEntry builds a directory entry.
func DirEntry() Entry { return Entry{Type: Dir} }

// LinkEntry builds a symlink entry.
func LinkEntry(target string) Entry { return Entry{Type: Symlink, Link: target} }

// cleanPath normalizes a manifest path: relative, slash-separated, no "." or
// ".." segments.
func cleanPath(p string) (string, error) {
	if p == "" || p == "." || p == "/" {
		return "", fmt.Errorf("manifest: empty path")
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("manifest: path %q must be relative", p)
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("manifest: path %q escapes the root", p)
	}
	return c, nil
}

// ancestors returns every strict ancestor of p, shallowest first.
func ancestors(p string) []string {
	var out []string
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		out = append([]string{d}, out...)
	}
	return out
}

// normalize validates and cleans every path in m.
func (m Manifest) normalize() (Manifest, error) {
	out := make(Manifest, len(m))
	for p, e := range m {
		c, err := cleanPath(p)
		if err != nil {
			return nil, err
		}
		if _, dup := out[c]; dup {
			return nil, fmt.Errorf("manifest: duplicate path %q", c)
		}
		out[c] = e
	}
	return out, nil
}

// checkConsistency ensures no listed non-directory entry has listed
// descendants.
func (m Manifest) checkConsistency() error {
	for p, e := range m {
		if e.Type == Dir {
			continue
		}
		prefix := p + "/"
		for q := range m {
			if strings.HasPrefix(q, prefix) {
				return fmt.Errorf("manifest: %q is a %s but has descendant %q", p, e.Type, q)
			}
		}
	}
	return nil
}

// withImplicitDirs returns a copy of m with every ancestor directory of every
// path materialized as a Dir entry (unless already present).
func (m Manifest) withImplicitDirs() Manifest {
	out := make(Manifest, len(m))
	for p, e := range m {
		out[p] = e
	}
	for p := range m {
		for _, d := range ancestors(p) {
			if _, ok := out[d]; !ok {
				out[d] = DirEntry()
			}
		}
	}
	return out
}

// sortedPaths returns the manifest paths ordered by (depth, path).
func sortedPaths(m Manifest) []string {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		di, dj := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
	return paths
}
