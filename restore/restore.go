// Package restore models restoring archive entries into a filesystem tree.
//
// Entries are staged one by one and published atomically by Commit. Hardlink
// entries may reference targets that appear later in the archive (forward
// references); references are resolved at commit time into shared inodes.
// Symlinks always remain independent content nodes and are never merged into
// a shared inode group. Hardlink cycles and hardlinks to directories are
// rejected, and a failed commit publishes no partial tree.
package restore

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// Kind enumerates archive entry kinds.
type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
	KindHardlink
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindSymlink:
		return "symlink"
	case KindHardlink:
		return "hardlink"
	}
	return "unknown"
}

// Sentinel failure reasons carried by PathError.Err.
var (
	ErrInvalidPath       = errors.New("restore: invalid path")
	ErrDuplicatePath     = errors.New("restore: duplicate path")
	ErrPathConflict      = errors.New("restore: path conflicts with non-directory entry")
	ErrDanglingTarget    = errors.New("restore: hardlink target not present in archive")
	ErrHardlinkCycle     = errors.New("restore: hardlink cycle detected")
	ErrHardlinkToDir     = errors.New("restore: hardlink target is a directory")
	ErrHardlinkToSymlink = errors.New("restore: hardlink target is a symlink")
)

// PathError associates a failure reason with the entry path that caused it.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string {
	return fmt.Sprintf("%s: %s", e.Err, e.Path)
}

// Entry is a single staged archive member. Content is used by KindFile,
// Target by KindSymlink and KindHardlink.
type Entry struct {
	Path    string
	Kind    Kind
	Content []byte
	Target  string
	Mode    os.FileMode
}

// Node is a resolved tree member. Hardlink entries surface as KindFile nodes
// sharing the inode of their resolved base; LinkFrom records the original
// reference for traceability.
type Node struct {
	Path     string
	Kind     Kind
	Inode    uint64
	Content  []byte
	Target   string
	Mode     os.FileMode
	LinkFrom string
}

// Snapshot is an immutable, fully resolved tree. It is only produced by a
// successful Commit; a failed commit publishes nothing.
type Snapshot struct {
	nodes   map[string]*Node
	byInode map[uint64][]string
}

// Node returns the resolved node at path.
func (s *Snapshot) Node(p string) (*Node, bool) {
	n, ok := s.nodes[p]
	return n, ok
}

// Len returns the number of nodes in the tree, including implicit directories.
func (s *Snapshot) Len() int { return len(s.nodes) }

// Paths returns all node paths in sorted order.
func (s *Snapshot) Paths() []string {
	out := make([]string, 0, len(s.nodes))
	for p := range s.nodes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// InodePaths returns the paths sharing the given inode, in sorted order.
func (s *Snapshot) InodePaths(inode uint64) []string {
	out := append([]string(nil), s.byInode[inode]...)
	sort.Strings(out)
	return out
}

// Shared reports whether the inode is referenced by more than one path.
func (s *Snapshot) Shared(inode uint64) bool {
	return len(s.byInode[inode]) > 1
}

// Graph returns the inode reference graph: inode ID to the sorted set of
// paths resolving to it. Inodes with more than one path are shared.
func (s *Snapshot) Graph() map[uint64][]string {
	out := make(map[uint64][]string, len(s.byInode))
	for ino, paths := range s.byInode {
		out[ino] = append([]string(nil), paths...)
		sort.Strings(out[ino])
	}
	return out
}

// Restorer stages archive entries and publishes them atomically.
type Restorer struct {
	entries map[string]*Entry
	order   []string
	dirs    map[string]bool // explicit and implicit directories
}

// New returns an empty Restorer.
func New() *Restorer {
	return &Restorer{
		entries: make(map[string]*Entry),
		dirs:    make(map[string]bool),
	}
}

// Len returns the number of staged entries.
func (r *Restorer) Len() int { return len(r.order) }

// Reset discards all staged entries.
func (r *Restorer) Reset() {
	r.entries = make(map[string]*Entry)
	r.order = nil
	r.dirs = make(map[string]bool)
}

// Stage adds one entry to the pending set. Path validity, duplicate paths,
// and prefix conflicts are checked immediately; hardlink targets are resolved
// later at Commit, so targets may not have been staged yet.
func (r *Restorer) Stage(e Entry) error {
	p, err := normalize(e.Path)
	if err != nil {
		return &PathError{Path: e.Path, Err: err}
	}
	if _, dup := r.entries[p]; dup {
		return &PathError{Path: p, Err: ErrDuplicatePath}
	}
	if e.Kind != KindDir && r.dirs[p] {
		return &PathError{Path: p, Err: ErrPathConflict}
	}
	for a := parent(p); a != ""; a = parent(a) {
		if pe, ok := r.entries[a]; ok && pe.Kind != KindDir {
			return &PathError{Path: p, Err: ErrPathConflict}
		}
	}
	if e.Kind == KindHardlink {
		if _, err := normalize(e.Target); err != nil {
			return &PathError{Path: p, Err: err}
		}
	}
	cp := e
	cp.Path = p
	r.entries[p] = &cp
	r.order = append(r.order, p)
	for a := parent(p); a != ""; a = parent(a) {
		r.dirs[a] = true
	}
	if e.Kind == KindDir {
		r.dirs[p] = true
	}
	return nil
}

// Commit resolves all staged references and, on success, returns the
// resulting snapshot. Resolution is atomic: any failure returns an error and
// publishes no partial tree, leaving the staged entries untouched so the
// caller can inspect, amend, and retry.
func (r *Restorer) Commit() (*Snapshot, error) {
	base, err := r.resolveBases()
	if err != nil {
		return nil, err
	}
	nodes := make(map[string]*Node, len(r.entries)+len(r.dirs))
	var next uint64 = 1

	// Implicit directories materialize as plain dir nodes.
	for d := range r.dirs {
		if _, explicit := r.entries[d]; !explicit {
			nodes[d] = &Node{Path: d, Kind: KindDir, Inode: next, Mode: 0755}
			next++
		}
	}
	// First pass: non-hardlink entries each get a fresh inode. Symlinks are
	// independent content nodes and are never deduplicated.
	for _, p := range r.order {
		e := r.entries[p]
		if e.Kind == KindHardlink {
			continue
		}
		n := &Node{Path: p, Kind: e.Kind, Inode: next, Mode: e.Mode}
		next++
		switch e.Kind {
		case KindFile:
			n.Content = e.Content
		case KindSymlink:
			n.Target = e.Target
		}
		nodes[p] = n
	}
	// Second pass: hardlinks adopt the inode and content of their base.
	for _, p := range r.order {
		e := r.entries[p]
		if e.Kind != KindHardlink {
			continue
		}
		b := nodes[base[p]]
		nodes[p] = &Node{
			Path:     p,
			Kind:     KindFile,
			Inode:    b.Inode,
			Content:  b.Content,
			Mode:     e.Mode,
			LinkFrom: base[p],
		}
	}
	byInode := make(map[uint64][]string)
	for p, n := range nodes {
		byInode[n.Inode] = append(byInode[n.Inode], p)
	}
	return &Snapshot{nodes: nodes, byInode: byInode}, nil
}

// resolveBases maps every staged hardlink path to the path of its ultimate
// non-hardlink base, rejecting dangling references, cycles, and bases that
// are directories or symlinks.
func (r *Restorer) resolveBases() (map[string]string, error) {
	base := make(map[string]string)
	for _, p := range r.order {
		e := r.entries[p]
		if e.Kind != KindHardlink {
			continue
		}
		seen := map[string]bool{p: true}
		cur, _ := normalize(e.Target)
		for {
			t, ok := r.entries[cur]
			if !ok {
				if r.dirs[cur] {
					return nil, &PathError{Path: p, Err: ErrHardlinkToDir}
				}
				return nil, &PathError{Path: p, Err: ErrDanglingTarget}
			}
			if t.Kind != KindHardlink {
				switch t.Kind {
				case KindDir:
					return nil, &PathError{Path: p, Err: ErrHardlinkToDir}
				case KindSymlink:
					return nil, &PathError{Path: p, Err: ErrHardlinkToSymlink}
				}
				base[p] = cur
				break
			}
			if seen[cur] {
				return nil, &PathError{Path: p, Err: ErrHardlinkCycle}
			}
			seen[cur] = true
			cur, _ = normalize(t.Target)
		}
	}
	return base, nil
}

// normalize cleans an archive path and rejects empty, absolute, or escaping
// paths.
func normalize(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') {
		return "", ErrInvalidPath
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", ErrInvalidPath
	}
	return c, nil
}

func parent(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}
