package netsec

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// ManifestTag domain-separates the manifest root digest.
const ManifestTag = "gsb-source-closure/v1/manifest\x00"

// PathStep records one repository boundary in a source entry's path chain.
// For a root-repository file the chain has one step. A file in a submodule has
// one step for the parent gitlink path and one step for the child path.
type PathStep struct {
	Repository RepositoryID
	Path       string
}

// SourceEntry is one reachable blob in the object closure. Path is relative to
// Repository; WorkspacePath is the path below the top-level workspace. Chain
// preserves every repository identity and relative path used to reach the
// blob. Mode and Digest are copied verbatim from the canonical tree.
type SourceEntry struct {
	Repository    RepositoryID
	Path          string
	WorkspacePath string
	Mode          Mode
	Kind          Kind
	Digest        Digest
	Size          int64
	Chain         []PathStep
}

// IsSymlink reports whether the entry is a symlink. Symlink content is stored
// in the blob as link-target bytes and is never followed by the builder.
func (e SourceEntry) IsSymlink() bool {
	return e.Mode == ModeSymlink
}

// SourceManifest is the complete source closure rooted at Root/Commit.
//
// TreeRoot is computed solely from Entries and therefore from the immutable
// object graph: it is a canonical workspace tree whose leaves carry each
// blob's mode and digest. ManifestRoot also binds repository identities and
// path chains.
type SourceManifest struct {
	Root         RepositoryID
	Commit       Digest
	Entries      []SourceEntry
	TreeRoot     Digest
	ManifestRoot Digest
}

// EntryByPath returns the entry at a workspace path.
func (m *SourceManifest) EntryByPath(path string) (SourceEntry, bool) {
	if m == nil {
		return SourceEntry{}, false
	}
	index := sort.Search(len(m.Entries), func(i int) bool {
		return m.Entries[i].WorkspacePath >= path
	})
	if index < len(m.Entries) && m.Entries[index].WorkspacePath == path {
		return m.Entries[index], true
	}
	return SourceEntry{}, false
}

func appendManifestString(buf *bytes.Buffer, value string) {
	putUint64(buf, uint64(len(value)))
	buf.WriteString(value)
}

// ComputeManifestRoot computes a deterministic digest over repository
// identities, path chains, workspace paths, modes, kinds, digests, and sizes.
func ComputeManifestRoot(entries []SourceEntry) (Digest, error) {
	ordered := append([]SourceEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].WorkspacePath < ordered[j].WorkspacePath
	})
	var buf bytes.Buffer
	putUint64(&buf, uint64(len(ordered)))
	for i, entry := range ordered {
		if entry.WorkspacePath == "" {
			return ZeroDigest, fmt.Errorf("%w: empty workspace path", ErrInvalidPath)
		}
		if i > 0 && ordered[i-1].WorkspacePath == entry.WorkspacePath {
			return ZeroDigest, fmt.Errorf("%w: duplicate workspace path %q", ErrPathConflict, entry.WorkspacePath)
		}
		if _, err := NormalizeRepoPath(entry.Path); err != nil {
			return ZeroDigest, err
		}
		if _, err := normalizeRelativePath(entry.WorkspacePath, false); err != nil {
			return ZeroDigest, err
		}
		if err := validateRepositoryID(entry.Repository); err != nil {
			return ZeroDigest, err
		}
		appendManifestString(&buf, string(entry.Repository))
		appendManifestString(&buf, entry.Path)
		appendManifestString(&buf, entry.WorkspacePath)
		putUint64(&buf, uint64(len(entry.Chain)))
		for _, step := range entry.Chain {
			if err := validateRepositoryID(step.Repository); err != nil {
				return ZeroDigest, err
			}
			if _, err := normalizeRelativePath(step.Path, false); err != nil {
				return ZeroDigest, err
			}
			appendManifestString(&buf, string(step.Repository))
			appendManifestString(&buf, step.Path)
		}
		appendManifestString(&buf, string(entry.Mode))
		putUint64(&buf, uint64(entry.Kind))
		buf.Write(entry.Digest[:])
		putUint64(&buf, uint64(entry.Size))
	}
	return hashDomain(ManifestTag, buf.Bytes()), nil
}

type manifestTreeNode struct {
	children map[string]*manifestTreeNode
	leaf     *SourceEntry
}

func newManifestTreeNode() *manifestTreeNode {
	return &manifestTreeNode{children: make(map[string]*manifestTreeNode)}
}

func (node *manifestTreeNode) insert(entry SourceEntry) error {
	parts := strings.Split(entry.WorkspacePath, "/")
	current := node
	for i, part := range parts {
		if err := validateComponent(part); err != nil {
			return err
		}
		last := i == len(parts)-1
		child := current.children[part]
		if last {
			if child != nil {
				if child.leaf != nil {
					return fmt.Errorf("%w: duplicate workspace path %q", ErrPathConflict, entry.WorkspacePath)
				}
				return fmt.Errorf("%w: file %q is also a directory", ErrPathConflict, entry.WorkspacePath)
			}
			copy := entry
			copy.Chain = append([]PathStep(nil), entry.Chain...)
			current.children[part] = &manifestTreeNode{leaf: &copy}
			continue
		}
		if child == nil {
			child = newManifestTreeNode()
			current.children[part] = child
		}
		if child.leaf != nil {
			return fmt.Errorf("%w: file %q is used as a parent directory", ErrPathConflict, strings.Join(parts[:i+1], "/"))
		}
		current = child
	}
	return nil
}

func (node *manifestTreeNode) digest() (Digest, error) {
	entries := make([]TreeEntry, 0, len(node.children))
	for name, child := range node.children {
		if child.leaf != nil {
			entries = append(entries, TreeEntry{
				Name:   name,
				Mode:   child.leaf.Mode,
				Kind:   child.leaf.Kind,
				Digest: child.leaf.Digest,
			})
			continue
		}
		digest, err := child.digest()
		if err != nil {
			return ZeroDigest, err
		}
		entries = append(entries, TreeEntry{
			Name:   name,
			Mode:   ModeTree,
			Kind:   KindTree,
			Digest: digest,
		})
	}
	return HashTree(entries)
}

// ComputeSourceTreeRoot computes the canonical workspace tree root from the
// manifest entries. It does not read a filesystem and does not need the object
// store: every leaf digest is already bound by the closure traversal.
func ComputeSourceTreeRoot(entries []SourceEntry) (Digest, error) {
	root := newManifestTreeNode()
	for _, entry := range entries {
		if entry.WorkspacePath == "" {
			return ZeroDigest, fmt.Errorf("%w: empty workspace path", ErrInvalidPath)
		}
		if _, err := normalizeRelativePath(entry.WorkspacePath, false); err != nil {
			return ZeroDigest, err
		}
		if entry.Digest.IsZero() {
			return ZeroDigest, fmt.Errorf("%w: zero blob digest at %q", ErrInvalidObject, entry.WorkspacePath)
		}
		_, impliedKind, err := canonicalMode(entry.Mode)
		if err != nil {
			return ZeroDigest, err
		}
		if impliedKind != KindBlob {
			return ZeroDigest, fmt.Errorf("%w: manifest leaf %q has non-blob mode %q", ErrInvalidObject, entry.WorkspacePath, entry.Mode)
		}
		if entry.Kind != KindInvalid && entry.Kind != KindBlob {
			return ZeroDigest, fmt.Errorf("%w: manifest leaf %q is not a blob", ErrInvalidObject, entry.WorkspacePath)
		}
		if err := root.insert(entry); err != nil {
			return ZeroDigest, err
		}
	}
	return root.digest()
}
