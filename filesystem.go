package netsec

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
)

// NodeType identifies a filesystem object.  Hard links are supported only for
// regular files.
type NodeType uint8

const (
	FileNode NodeType = iota + 1
	DirectoryNode
)

// Aliases for callers that prefer explicit names at the call site.
const (
	TypeFile      = FileNode
	TypeDirectory = DirectoryNode
)

// Node is a snapshot of filesystem metadata.  Policy is a copy, so mutating the
// returned value cannot modify the filesystem.
type Node struct {
	Path   string
	Name   string
	Type   NodeType
	Policy *Policy
	Inode  uint64
	Links  uint64
}

// IsDir reports whether the node is a directory.
func (n Node) IsDir() bool {
	return n.Type == DirectoryNode
}

// Encrypted reports whether the node is protected by an fscrypt policy.
func (n Node) Encrypted() bool {
	return n.Policy != nil
}

// FileSystem is an in-memory model of fscrypt policy inheritance and
// cross-directory namespace operations.  It is safe for concurrent use.
type FileSystem struct {
	mu        sync.RWMutex
	nextInode uint64
	root      *inode
}

// FS is a short alias for FileSystem.
type FS = FileSystem

type inode struct {
	id       uint64
	typ      NodeType
	policy   *Policy
	links    uint64
	children map[string]*entry
}

type entry struct {
	name  string
	inode *inode
}

// NewFileSystem returns a filesystem containing only an empty plaintext root
// directory.
func NewFileSystem() *FileSystem {
	return &FileSystem{
		nextInode: 2,
		root: &inode{
			id:       1,
			typ:      DirectoryNode,
			links:    1,
			children: make(map[string]*entry),
		},
	}
}

// NewFS is an alias for NewFileSystem.
func NewFS() *FileSystem {
	return NewFileSystem()
}

// New is an alias for NewFileSystem.
func New() *FileSystem {
	return NewFileSystem()
}

// Create creates a regular file.  Its policy is inherited from the parent
// directory, including a nil (plaintext) policy.
func (fs *FileSystem) Create(name string) error {
	return fs.create(name, FileNode)
}

// CreateFile is an alias for Create.
func (fs *FileSystem) CreateFile(name string) error {
	return fs.Create(name)
}

// Mkdir creates an empty directory.  Its policy is inherited from the parent
// directory.
func (fs *FileSystem) Mkdir(name string) error {
	return fs.create(name, DirectoryNode)
}

// CreateDir is an alias for Mkdir.
func (fs *FileSystem) CreateDir(name string) error {
	return fs.Mkdir(name)
}

// MakeDir is an alias for Mkdir.
func (fs *FileSystem) MakeDir(name string) error {
	return fs.Mkdir(name)
}

// MkdirWithPolicy atomically creates an empty directory with policy.  If the
// parent is already encrypted, policy must exactly match the inherited parent
// policy.  If the parent is plaintext, this is equivalent to creating an empty
// directory and then setting its policy before it can receive children.
func (fs *FileSystem) MkdirWithPolicy(name string, policy Policy) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	clean, components, err := normalizePath(name)
	if err != nil {
		return wrapPathError("mkdir", name, err)
	}
	if len(components) == 0 {
		return wrapPathError("mkdir", clean, ErrExists)
	}
	parent, base, err := fs.resolveParent(components)
	if err != nil {
		return wrapPathError("mkdir", clean, err)
	}
	if _, ok := parent.children[base]; ok {
		return wrapPathError("mkdir", clean, ErrExists)
	}

	effective := clonePolicy(parent.policy)
	if effective != nil {
		if !effective.Equal(policy) {
			return wrapPathError("mkdir", clean, ErrPolicyMismatch)
		}
	} else {
		effective = clonePolicy(&policy)
	}

	child := &inode{
		id:       fs.allocateInode(),
		typ:      DirectoryNode,
		policy:   effective,
		links:    1,
		children: make(map[string]*entry),
	}
	parent.children[base] = &entry{name: base, inode: child}
	return nil
}

// SetPolicy sets the policy of an empty directory that does not have one yet.
// It never changes an existing policy, even when the requested value is equal.
func (fs *FileSystem) SetPolicy(name string, policy Policy) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	clean, components, err := normalizePath(name)
	if err != nil {
		return wrapPathError("set policy", name, err)
	}
	dir, err := fs.walk(components)
	if err != nil {
		return wrapPathError("set policy", clean, err)
	}
	if dir.typ != DirectoryNode {
		return wrapPathError("set policy", clean, ErrNotDirectory)
	}
	if dir.policy != nil {
		return wrapPathError("set policy", clean, ErrPolicyAlreadySet)
	}
	if len(dir.children) != 0 {
		return wrapPathError("set policy", clean, ErrNotEmpty)
	}

	dir.policy = clonePolicy(&policy)
	return nil
}

// SetDirPolicy is an alias for SetPolicy.
func (fs *FileSystem) SetDirPolicy(name string, policy Policy) error {
	return fs.SetPolicy(name, policy)
}

// Stat returns filesystem metadata for name.
func (fs *FileSystem) Stat(name string) (*Node, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	clean, components, err := normalizePath(name)
	if err != nil {
		return nil, wrapPathError("stat", name, err)
	}
	node, err := fs.walk(components)
	if err != nil {
		return nil, wrapPathError("stat", clean, err)
	}

	base := "/"
	if len(components) != 0 {
		base = components[len(components)-1]
	}
	return &Node{
		Path:   clean,
		Name:   base,
		Type:   node.typ,
		Policy: clonePolicy(node.policy),
		Inode:  node.id,
		Links:  node.links,
	}, nil
}

// Lookup is an alias for Stat.
func (fs *FileSystem) Lookup(name string) (*Node, error) {
	return fs.Stat(name)
}

// PolicyAt returns a copy of the policy attached to name.  A nil policy with a
// nil error means the object is plaintext.
func (fs *FileSystem) PolicyAt(name string) (*Policy, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	clean, components, err := normalizePath(name)
	if err != nil {
		return nil, wrapPathError("policy", name, err)
	}
	node, err := fs.walk(components)
	if err != nil {
		return nil, wrapPathError("policy", clean, err)
	}
	return clonePolicy(node.policy), nil
}

// PolicyOf is an alias for PolicyAt.
func (fs *FileSystem) PolicyOf(name string) (*Policy, error) {
	return fs.PolicyAt(name)
}

// IsEncrypted reports whether name is protected by a policy.
func (fs *FileSystem) IsEncrypted(name string) (bool, error) {
	policy, err := fs.PolicyAt(name)
	return policy != nil, err
}

// Rename moves oldName to newName.  The source and destination parent
// directories must have exactly equal policies, and the moved inode must have
// the same policy as the destination parent.  Consequently plaintext entries
// cannot enter encrypted directories and encrypted entries cannot enter
// plaintext directories.
//
// A file may replace a file.  A directory may replace an empty directory of the
// same policy domain.  All validation is completed before the namespace is
// modified.
func (fs *FileSystem) Rename(oldName, newName string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.renameLocked(oldName, newName)
}

// Move is an alias for Rename.
func (fs *FileSystem) Move(oldName, newName string) error {
	return fs.Rename(oldName, newName)
}

func (fs *FileSystem) renameLocked(oldName, newName string) error {
	oldClean, oldComponents, err := normalizePath(oldName)
	if err != nil {
		return wrapPathError("rename", oldName, err)
	}
	newClean, newComponents, err := normalizePath(newName)
	if err != nil {
		return wrapPathError("rename", newName, err)
	}
	if len(oldComponents) == 0 || len(newComponents) == 0 {
		return wrapPathError("rename", oldClean, ErrInvalidPath)
	}

	oldParent, oldBase, oldEntry, err := fs.resolveEntry(oldComponents)
	if err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, err)
	}
	if oldClean == newClean {
		return nil
	}

	newParent, newBase, err := fs.resolveParent(newComponents)
	if err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, err)
	}
	if err := placementError(oldParent.policy, newParent.policy, oldEntry.inode.policy); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, err)
	}
	if oldEntry.inode.typ == DirectoryNode && isSubtree(oldEntry.inode, newParent) {
		return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, ErrInvalidOperation)
	}

	destination := newParent.children[newBase]
	if destination != nil {
		// POSIX rename is a successful no-op when both names reference the same
		// inode, such as two hard links to one file.
		if destination.inode == oldEntry.inode {
			return nil
		}
		if oldEntry.inode.typ == DirectoryNode {
			if destination.inode.typ != DirectoryNode {
				return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, ErrNotDirectory)
			}
			if len(destination.inode.children) != 0 {
				return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, ErrNotEmpty)
			}
		} else if destination.inode.typ == DirectoryNode {
			return fmt.Errorf("rename %q to %q: %w", oldClean, newClean, ErrIsDirectory)
		}
	}

	if destination != nil {
		delete(newParent.children, newBase)
		unlink(destination.inode)
	}
	delete(oldParent.children, oldBase)
	oldEntry.name = newBase
	newParent.children[newBase] = oldEntry
	return nil
}

// Link creates a hard link newName to the regular file oldName.  The source and
// destination parent directories must have exactly equal policies, and the
// linked inode must itself belong to that same policy.
func (fs *FileSystem) Link(oldName, newName string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	oldClean, oldComponents, err := normalizePath(oldName)
	if err != nil {
		return wrapPathError("link", oldName, err)
	}
	newClean, newComponents, err := normalizePath(newName)
	if err != nil {
		return wrapPathError("link", newName, err)
	}
	if len(oldComponents) == 0 || len(newComponents) == 0 {
		return wrapPathError("link", oldClean, ErrInvalidPath)
	}

	oldParent, _, oldEntry, err := fs.resolveEntry(oldComponents)
	if err != nil {
		return fmt.Errorf("link %q to %q: %w", oldClean, newClean, err)
	}
	if oldEntry.inode.typ == DirectoryNode {
		return fmt.Errorf("link %q to %q: %w", oldClean, newClean, ErrHardLinkToDirectory)
	}

	newParent, newBase, err := fs.resolveParent(newComponents)
	if err != nil {
		return fmt.Errorf("link %q to %q: %w", oldClean, newClean, err)
	}
	if _, ok := newParent.children[newBase]; ok {
		return fmt.Errorf("link %q to %q: %w", oldClean, newClean, ErrExists)
	}
	if err := placementError(oldParent.policy, newParent.policy, oldEntry.inode.policy); err != nil {
		return fmt.Errorf("link %q to %q: %w", oldClean, newClean, err)
	}

	newParent.children[newBase] = &entry{name: newBase, inode: oldEntry.inode}
	oldEntry.inode.links++
	return nil
}

// HardLink is an alias for Link.
func (fs *FileSystem) HardLink(oldName, newName string) error {
	return fs.Link(oldName, newName)
}

// Exchange atomically swaps the entries at first and second, equivalent to
// renameat2(RENAME_EXCHANGE).  Both parent directories must have exactly equal
// policies, both inodes must belong to those policies, and neither operation may
// introduce a directory cycle.  Validation is completed for both directions
// before either entry is changed.
func (fs *FileSystem) Exchange(first, second string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	firstClean, firstComponents, err := normalizePath(first)
	if err != nil {
		return wrapPathError("exchange", first, err)
	}
	secondClean, secondComponents, err := normalizePath(second)
	if err != nil {
		return wrapPathError("exchange", second, err)
	}
	if len(firstComponents) == 0 || len(secondComponents) == 0 {
		return wrapPathError("exchange", firstClean, ErrInvalidPath)
	}

	firstParent, firstBase, firstEntry, err := fs.resolveEntry(firstComponents)
	if err != nil {
		return fmt.Errorf("exchange %q and %q: %w", firstClean, secondClean, err)
	}
	secondParent, secondBase, secondEntry, err := fs.resolveEntry(secondComponents)
	if err != nil {
		return fmt.Errorf("exchange %q and %q: %w", firstClean, secondClean, err)
	}
	if firstClean == secondClean {
		return nil
	}
	firstPlacement := placementError(firstParent.policy, secondParent.policy, firstEntry.inode.policy)
	secondPlacement := placementError(secondParent.policy, firstParent.policy, secondEntry.inode.policy)
	if err := errors.Join(firstPlacement, secondPlacement); err != nil {
		return fmt.Errorf("exchange %q and %q: %w", firstClean, secondClean, err)
	}
	if (firstEntry.inode.typ == DirectoryNode && isSubtree(firstEntry.inode, secondParent)) ||
		(secondEntry.inode.typ == DirectoryNode && isSubtree(secondEntry.inode, firstParent)) {
		return fmt.Errorf("exchange %q and %q: %w", firstClean, secondClean, ErrInvalidOperation)
	}

	delete(firstParent.children, firstBase)
	delete(secondParent.children, secondBase)
	firstEntry.name = secondBase
	secondEntry.name = firstBase
	secondParent.children[secondBase] = firstEntry
	firstParent.children[firstBase] = secondEntry
	return nil
}

// RenameExchange is an alias for Exchange.
func (fs *FileSystem) RenameExchange(first, second string) error {
	return fs.Exchange(first, second)
}

func (fs *FileSystem) create(name string, typ NodeType) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	clean, components, err := normalizePath(name)
	if err != nil {
		return wrapPathError("create", name, err)
	}
	if len(components) == 0 {
		return wrapPathError("create", clean, ErrExists)
	}
	parent, base, err := fs.resolveParent(components)
	if err != nil {
		return wrapPathError("create", clean, err)
	}
	if _, ok := parent.children[base]; ok {
		return wrapPathError("create", clean, ErrExists)
	}

	child := &inode{
		id:     fs.allocateInode(),
		typ:    typ,
		policy: clonePolicy(parent.policy),
		links:  1,
	}
	if typ == DirectoryNode {
		child.children = make(map[string]*entry)
	}
	parent.children[base] = &entry{name: base, inode: child}
	return nil
}

func (fs *FileSystem) allocateInode() uint64 {
	id := fs.nextInode
	fs.nextInode++
	return id
}

func (fs *FileSystem) walk(components []string) (*inode, error) {
	current := fs.root
	for _, component := range components {
		if current.typ != DirectoryNode {
			return nil, ErrNotDirectory
		}
		next := current.children[component]
		if next == nil {
			return nil, ErrNotFound
		}
		current = next.inode
	}
	return current, nil
}

func (fs *FileSystem) resolveParent(components []string) (*inode, string, error) {
	if len(components) == 0 {
		return nil, "", ErrInvalidPath
	}
	parent, err := fs.walk(components[:len(components)-1])
	if err != nil {
		return nil, "", err
	}
	if parent.typ != DirectoryNode {
		return nil, "", ErrNotDirectory
	}
	return parent, components[len(components)-1], nil
}

func (fs *FileSystem) resolveEntry(components []string) (*inode, string, *entry, error) {
	parent, base, err := fs.resolveParent(components)
	if err != nil {
		return nil, "", nil, err
	}
	node := parent.children[base]
	if node == nil {
		return nil, "", nil, ErrNotFound
	}
	return parent, base, node, nil
}

func placementError(sourcePolicy, destinationPolicy, nodePolicy *Policy) error {
	var problems []error
	if !PoliciesEqual(sourcePolicy, destinationPolicy) ||
		!PoliciesEqual(nodePolicy, destinationPolicy) {
		problems = append(problems, ErrPolicyMismatch)
	}
	if nodePolicy == nil && destinationPolicy != nil {
		problems = append(problems, ErrPlaintextIntoEncrypted)
	}
	if nodePolicy != nil && destinationPolicy == nil {
		problems = append(problems, ErrEncryptedIntoPlaintext)
	}
	return errors.Join(problems...)
}

func unlink(node *inode) {
	if node.links != 0 {
		node.links--
	}
}

func isSubtree(root, target *inode) bool {
	if root == target {
		return true
	}
	if root.typ != DirectoryNode {
		return false
	}
	for _, child := range root.children {
		if isSubtree(child.inode, target) {
			return true
		}
	}
	return false
}

func normalizePath(name string) (string, []string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", nil, ErrInvalidPath
	}
	if name == "" {
		return "/", nil, nil
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." {
			return "", nil, ErrInvalidPath
		}
	}

	clean := path.Clean(name)
	if clean == "/" {
		return clean, nil, nil
	}
	return clean, strings.Split(strings.TrimPrefix(clean, "/"), "/"), nil
}

func wrapPathError(operation, name string, err error) error {
	if name == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s %q: %w", operation, name, err)
}
