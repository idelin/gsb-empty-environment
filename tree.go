package safeunpack

import (
	"math"
	"sort"
	"strings"
)

// EntryType describes the only archive members materialized by this package.
type EntryType uint8

const (
	TypeDirectory EntryType = iota
	TypeRegular
	TypeSymlink
	TypeHardlink
)

const (
	KindDirectory = TypeDirectory
	KindFile      = TypeRegular
	KindRegular   = TypeRegular
	KindSymlink   = TypeSymlink
	KindHardlink  = TypeHardlink
)

// String returns a stable, human-readable entry type.
func (t EntryType) String() string {
	switch t {
	case TypeDirectory:
		return "directory"
	case TypeRegular:
		return "file"
	case TypeSymlink:
		return "symlink"
	case TypeHardlink:
		return "hardlink"
	default:
		return "unknown"
	}
}

// Node is one entry in the exact in-memory target tree. Data is populated for
// regular files (and is nil for directories and links). LinkTarget retains the
// archive-supplied relative target; ResolvedTarget is the validated canonical
// path below the virtual root.
type Node struct {
	Name           string
	Path           string
	Type           EntryType
	Mode           uint32
	Size           int64
	Data           []byte
	LinkTarget     string
	ResolvedTarget string

	children map[string]*Node
	implicit bool
}

// Child returns an immediate child using archive case-sensitive semantics.
func (n *Node) Child(name string) (*Node, bool) {
	if n == nil || n.children == nil {
		return nil, false
	}
	c, ok := n.children[name]
	return c, ok
}

// Children returns immediate children sorted by path for deterministic
// inspection.
func (n *Node) Children() []*Node {
	if n == nil {
		return nil
	}
	out := make([]*Node, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Lookup resolves a canonical relative path from this node.
func (n *Node) Lookup(path string) (*Node, bool) {
	if n == nil {
		return nil, false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	cur := n
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return nil, false
		}
		next, ok := cur.children[part]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// Entry describes one materialized archive member in original archive order.
// PAX extension records are metadata and are not listed.
type Entry struct {
	Path           string
	Type           EntryType
	Mode           uint32
	Size           int64
	LinkTarget     string
	ResolvedTarget string
	Offset         int64
	CompressedSize int64
}

// Result is the validated virtual unpack result.
type Result struct {
	Root       *Node
	Entries    []Entry
	Files      int
	Dirs       int
	Symlinks   int
	Hardlinks  int
	TotalBytes int64
	FileBytes  int64
	Compressed int64
	MaxRatio   float64
	Format     Format
}

// Report is an alias for Result for callers that use reporting terminology.
type Report = Result

// Lookup returns a node by canonical relative path.
func (r *Result) Lookup(path string) (*Node, bool) {
	if r == nil || r.Root == nil {
		return nil, false
	}
	return r.Root.Lookup(path)
}

type treeState struct {
	root     *Node
	byPath   map[string]*Node
	byFold   map[string]*Node
	explicit map[string]bool
	entries  []Entry

	totalBytes int64
	fileBytes  int64
	files      int
	dirs       int
	symlinks   int
	hardlinks  int
}

func newTree() *treeState {
	root := &Node{Name: "", Path: "", Type: TypeDirectory, Mode: 0o755}
	root.children = make(map[string]*Node)
	return &treeState{
		root:     root,
		byPath:   map[string]*Node{"": root},
		byFold:   map[string]*Node{"": root},
		explicit: make(map[string]bool),
	}
}

func (t *treeState) result(format Format, compressed int64, maxRatio float64) *Result {
	entries := make([]Entry, len(t.entries))
	copy(entries, t.entries)
	return &Result{
		Root:       t.root,
		Entries:    entries,
		Files:      t.files,
		Dirs:       t.dirs,
		Symlinks:   t.symlinks,
		Hardlinks:  t.hardlinks,
		TotalBytes: t.totalBytes,
		FileBytes:  t.fileBytes,
		Compressed: compressed,
		MaxRatio:   maxRatio,
		Format:     format,
	}
}

func foldPath(path string) string {
	return strings.ToLower(path)
}

func (t *treeState) duplicate(path string) error {
	if other, ok := t.byFold[foldPath(path)]; ok && other.Path != path {
		return wrapError(ErrDuplicatePath, path, -1, "case-insensitive collision with %q", other.Path)
	}
	return wrapError(ErrDuplicatePath, path, -1, "entry already exists")
}

func (t *treeState) ensureParents(parts []string) (*Node, error) {
	cur := t.root
	for i := 0; i < len(parts)-1; i++ {
		comp := parts[i]
		nextPath := strings.Join(parts[:i+1], "/")
		if existing, ok := cur.children[comp]; ok {
			switch existing.Type {
			case TypeDirectory:
				cur = existing
				continue
			case TypeSymlink:
				return nil, wrapError(ErrSymlinkTraversal, nextPath, -1,
					"parent %q is an already-created symbolic link", nextPath)
			default:
				return nil, wrapError(ErrUnsafePath, nextPath, -1,
					"parent %q is not a directory", nextPath)
			}
		}
		if other, ok := t.byFold[foldPath(nextPath)]; ok {
			return nil, wrapError(ErrDuplicatePath, nextPath, -1,
				"case-insensitive collision with %q", other.Path)
		}
		n := &Node{
			Name:     comp,
			Path:     nextPath,
			Type:     TypeDirectory,
			Mode:     0o755,
			implicit: true,
			children: make(map[string]*Node),
		}
		cur.children[comp] = n
		t.byPath[nextPath] = n
		t.byFold[foldPath(nextPath)] = n
		cur = n
	}
	return cur, nil
}

func (t *treeState) addDirectory(path string, parts []string, mode uint32, offset int64) error {
	parent, err := t.ensureParents(parts)
	if err != nil {
		return err
	}
	final := parts[len(parts)-1]
	if existing, ok := parent.children[final]; ok {
		if existing.Type == TypeDirectory && existing.implicit && !t.explicit[path] {
			existing.implicit = false
			existing.Mode = mode
			t.explicit[path] = true
			t.dirs++
			t.entries = append(t.entries, Entry{Path: path, Type: TypeDirectory, Mode: mode, Offset: offset})
			return nil
		}
		return wrapError(ErrDuplicatePath, path, offset, "directory already exists")
	}
	if other, ok := t.byFold[foldPath(path)]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "case-insensitive collision with %q", other.Path)
	}
	n := &Node{
		Name:     final,
		Path:     path,
		Type:     TypeDirectory,
		Mode:     mode,
		children: make(map[string]*Node),
	}
	parent.children[final] = n
	t.byPath[path] = n
	t.byFold[foldPath(path)] = n
	t.explicit[path] = true
	t.dirs++
	t.entries = append(t.entries, Entry{Path: path, Type: TypeDirectory, Mode: mode, Offset: offset})
	return nil
}

func (t *treeState) preflightNonDirectory(path string, parts []string, offset int64) error {
	parent, err := t.ensureParents(parts)
	if err != nil {
		return err
	}
	final := parts[len(parts)-1]
	if _, ok := parent.children[final]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "entry overwrites an existing path")
	}
	if other, ok := t.byFold[foldPath(path)]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "case-insensitive collision with %q", other.Path)
	}
	return nil
}

func (t *treeState) addRegular(path string, parts []string, mode uint32, data []byte, offset int64) error {
	parent, err := t.ensureParents(parts)
	if err != nil {
		return err
	}
	final := parts[len(parts)-1]
	if _, ok := parent.children[final]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "file overwrites an existing path")
	}
	if other, ok := t.byFold[foldPath(path)]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "case-insensitive collision with %q", other.Path)
	}
	n := &Node{
		Name: final,
		Path: path,
		Type: TypeRegular,
		Mode: mode,
		Size: int64(len(data)),
		Data: data,
	}
	parent.children[final] = n
	t.byPath[path] = n
	t.byFold[foldPath(path)] = n
	t.explicit[path] = true
	t.files++
	t.fileBytes += int64(len(data))
	t.entries = append(t.entries, Entry{
		Path: path, Type: TypeRegular, Mode: mode, Size: int64(len(data)), Offset: offset,
	})
	return nil
}

func (t *treeState) addSymlink(path string, parts []string, mode uint32, target, resolved string, size, offset int64) error {
	parent, err := t.ensureParents(parts)
	if err != nil {
		return err
	}
	final := parts[len(parts)-1]
	if _, ok := parent.children[final]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "symlink overwrites an existing path")
	}
	if other, ok := t.byFold[foldPath(path)]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "case-insensitive collision with %q", other.Path)
	}
	n := &Node{
		Name:           final,
		Path:           path,
		Type:           TypeSymlink,
		Mode:           mode,
		Size:           size,
		LinkTarget:     target,
		ResolvedTarget: resolved,
	}
	parent.children[final] = n
	t.byPath[path] = n
	t.byFold[foldPath(path)] = n
	t.explicit[path] = true
	t.symlinks++
	t.entries = append(t.entries, Entry{
		Path: path, Type: TypeSymlink, Mode: mode, Size: size,
		LinkTarget: target, ResolvedTarget: resolved, Offset: offset,
	})
	return nil
}

func (t *treeState) addHardlink(path string, parts []string, mode uint32, target string, targetNode *Node, offset int64) error {
	parent, err := t.ensureParents(parts)
	if err != nil {
		return err
	}
	final := parts[len(parts)-1]
	if _, ok := parent.children[final]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "hardlink overwrites an existing path")
	}
	if other, ok := t.byFold[foldPath(path)]; ok {
		return wrapError(ErrDuplicatePath, path, offset, "case-insensitive collision with %q", other.Path)
	}
	resolved := targetNode.Path
	n := &Node{
		Name:           final,
		Path:           path,
		Type:           TypeHardlink,
		Mode:           mode,
		Size:           targetNode.Size,
		LinkTarget:     target,
		ResolvedTarget: resolved,
	}
	parent.children[final] = n
	t.byPath[path] = n
	t.byFold[foldPath(path)] = n
	t.explicit[path] = true
	t.hardlinks++
	t.entries = append(t.entries, Entry{
		Path: path, Type: TypeHardlink, Mode: mode, Size: targetNode.Size,
		LinkTarget: target, ResolvedTarget: resolved, Offset: offset,
	})
	return nil
}

func (t *treeState) charge(size, maxFile, maxTotal int64) error {
	if size < 0 {
		return wrapError(ErrMalformed, "", -1, "negative decoded size")
	}
	if maxTotal >= 0 && t.totalBytes > maxTotal {
		return wrapError(ErrLimitExceeded, "", -1,
			"total decoded bytes already exceed limit %d", maxTotal)
	}
	if size > math.MaxInt64-t.totalBytes {
		return wrapError(ErrLimitExceeded, "", -1, "total decoded byte counter overflow")
	}
	if maxFile >= 0 && size > maxFile {
		return wrapError(ErrLimitExceeded, "", -1, "member size %d exceeds per-file limit %d", size, maxFile)
	}
	if maxTotal >= 0 && (size > maxTotal-t.totalBytes) {
		return wrapError(ErrLimitExceeded, "", -1,
			"total decoded bytes would exceed limit %d", maxTotal)
	}
	t.totalBytes += size
	return nil
}

// resolveHardlinkTarget validates a TAR hard link target relative to the root.
// Intermediate ".." components are accepted only while they remain below the
// root; any symbolic link on the target path is rejected.
func (t *treeState) resolveHardlinkTarget(raw string, maxPath int) (*Node, error) {
	parts, err := splitAndValidateLinkTarget(raw, maxPath, true)
	if err != nil {
		return nil, err
	}
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "..":
			if len(stack) == 0 {
				return nil, wrapError(ErrHardlink, raw, -1, "hardlink target traverses above root")
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	if len(stack) == 0 {
		return nil, wrapError(ErrHardlink, raw, -1, "hardlink target is the archive root")
	}

	cur := t.root
	for i, part := range stack {
		next, ok := cur.children[part]
		if !ok {
			path := strings.Join(stack[:i+1], "/")
			return nil, wrapError(ErrHardlink, raw, -1, "hardlink target %q does not exist", path)
		}
		if next.Type == TypeSymlink {
			return nil, wrapError(ErrHardlink, raw, -1, "hardlink target points to symbolic link %q", next.Path)
		}
		cur = next
	}
	if cur.Type != TypeRegular && cur.Type != TypeHardlink {
		return nil, wrapError(ErrHardlink, raw, -1, "hardlink target is not a regular file")
	}
	return cur, nil
}

// resolveSymlinkTarget resolves a relative symbolic-link target against the
// entry's parent, following previously-created in-root symbolic links and
// rejecting any traversal above the virtual root or a cycle.
func (t *treeState) resolveSymlinkTarget(entryPath string, parentParts []string, raw string, maxPath int) (string, error) {
	parts, err := splitAndValidateLinkTarget(raw, maxPath, true)
	if err != nil {
		return "", err
	}
	cur := append([]string(nil), parentParts...)
	queue := append([]string(nil), parts...)
	expansions := 0
	for len(queue) > 0 {
		part := queue[0]
		queue = queue[1:]
		switch part {
		case "..":
			if len(cur) == 0 {
				return "", wrapError(ErrLinkEscape, entryPath, -1, "link target %q traverses above root", raw)
			}
			cur = cur[:len(cur)-1]
			continue
		}

		cur = append(cur, part)
		path := strings.Join(cur, "/")
		node, ok := t.byPath[path]
		if !ok || node.Type != TypeSymlink {
			continue
		}
		expansions++
		if expansions > 64 {
			return "", wrapError(ErrLinkEscape, entryPath, -1, "symbolic link target chain is cyclic or too deep")
		}
		linkParts, err := splitAndValidateLinkTarget(node.LinkTarget, maxPath, true)
		if err != nil {
			return "", wrapError(ErrLinkEscape, entryPath, -1, "existing link %q has unsafe target", node.Path)
		}
		cur = cur[:len(cur)-1]
		queue = append(append([]string(nil), linkParts...), queue...)
	}

	resolved := strings.Join(cur, "/")
	if resolved == entryPath {
		return "", wrapError(ErrLinkEscape, entryPath, -1, "symbolic link resolves to itself")
	}
	return resolved, nil
}
