// Package restore models the restoration of an archive into a filesystem
// tree. Archive entries may contain hard links whose targets have not been
// seen yet; the restorer stages such references and resolves them against
// an inode reference graph only when the batch is finalized. Symbolic links
// always remain independent content and never share an inode. Hard-link
// cycles and hard links to directories are rejected, and a rejected batch
// never publishes a half-built tree: the previously committed state is
// left untouched.
package restore

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// EntryType classifies an archive entry.
type EntryType int

const (
	// FileEntry is a regular file carrying its own content.
	FileEntry EntryType = iota
	// DirEntry is a directory.
	DirEntry
	// SymlinkEntry is a symbolic link; Target holds the link text.
	SymlinkEntry
	// HardlinkEntry is a hard link; Target names another archive path
	// that may appear before or after this entry.
	HardlinkEntry
)

// Entry is a single archive record.
type Entry struct {
	Path    string
	Type    EntryType
	Content []byte // FileEntry only
	Target  string // SymlinkEntry and HardlinkEntry only
}

// NodeKind classifies a materialized tree node.
type NodeKind int

const (
	DirNode NodeKind = iota
	FileNode
	SymlinkNode
)

// Inode is shared file content. Every hard link to the same file resolves
// to the same *Inode.
type Inode struct {
	ID      int
	Content []byte
	Links   int // number of tree nodes pointing at this inode
}

// Node is a materialized tree entry.
type Node struct {
	Kind   NodeKind
	Inode  *Inode // FileNode only
	Target string // SymlinkNode only
}

// Tree is an immutable, committed view of restored paths.
type Tree struct {
	nodes  map[string]*Node
	inodes map[int]*Inode
}

// Node returns the node at path, or nil.
func (t *Tree) Node(path string) *Node { return t.nodes[path] }

// Paths returns all materialized paths in sorted order.
func (t *Tree) Paths() []string {
	out := make([]string, 0, len(t.nodes))
	for p := range t.nodes {
		out = append(out, p)
	}
	sortStrings(out)
	return out
}

// Inode returns the inode with the given ID, or nil.
func (t *Tree) Inode(id int) *Inode { return t.inodes[id] }

// LinkCount reports how many nodes share the inode at path (0 if the path
// is absent or not a file).
func (t *Tree) LinkCount(path string) int {
	n := t.nodes[path]
	if n == nil || n.Kind != FileNode {
		return 0
	}
	return n.Inode.Links
}

// Errors returned by Apply. Wrapped in *OpError for context.
var (
	ErrInvalidPath    = errors.New("restore: invalid path")
	ErrDuplicatePath  = errors.New("restore: duplicate path")
	ErrParentNotDir   = errors.New("restore: parent is not a directory")
	ErrDanglingLink   = errors.New("restore: hard link target does not exist")
	ErrLinkToDir      = errors.New("restore: hard link to a directory")
	ErrLinkCycle      = errors.New("restore: hard link cycle")
	ErrMalformedEntry = errors.New("restore: malformed entry")
)

// OpError ties a failure to the entry path that caused it.
type OpError struct {
	Path string
	Err  error
}

func (e *OpError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }

// Restorer applies batches of archive entries. Each Apply either commits
// atomically or rolls back, leaving the previously committed tree intact.
type Restorer struct {
	committed *Tree
}

// NewRestorer returns a Restorer with an empty committed tree.
func NewRestorer() *Restorer {
	return &Restorer{committed: newTree()}
}

// Tree returns the currently committed tree. It is never mutated by later
// Apply calls.
func (r *Restorer) Tree() *Tree { return r.committed }

// Apply stages entries on top of the committed tree, resolves all pending
// hard-link references against the inode reference graph, and publishes the
// result atomically. On any error the committed tree is left untouched and
// no partial state leaks out.
func (r *Restorer) Apply(entries []Entry) error {
	st := r.committed.stage()
	for _, e := range entries {
		if err := st.add(e); err != nil {
			return err // staged state is discarded: nothing is published
		}
	}
	if err := st.resolveLinks(); err != nil {
		return err
	}
	r.committed = st.freeze()
	return nil
}

func newTree() *Tree {
	return &Tree{nodes: map[string]*Node{}, inodes: map[int]*Inode{}}
}

// cleanPath validates and normalizes an archive path.
func cleanPath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") {
		return "", ErrInvalidPath
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.Contains(c, "/../") {
		return "", ErrInvalidPath
	}
	return c, nil
}
