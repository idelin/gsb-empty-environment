package netsec

import (
	"fmt"
	pathpkg "path"
	"sort"
	"strings"
	"sync"
)

// Kind identifies the type of a filesystem node.
type Kind uint8

const (
	KindFile Kind = iota + 1
	KindDir
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "directory"
	default:
		return "unknown"
	}
}

// NodeID identifies an inode. Hard links to the same file have the same ID.
type NodeID uint64

// Info is a snapshot of a node's policy and identity.
type Info struct {
	ID        NodeID
	Kind      Kind
	Encrypted bool
	Policy    Policy
	// Links is the number of hard links for a regular file. It is 1 for
	// directories.
	Links uint64
}

// DirEntry describes one name in a directory.
type DirEntry struct {
	Name string
	Kind Kind
	ID   NodeID
}

type node struct {
	id       NodeID
	kind     Kind
	parent   *node // set only for directories
	children map[string]*node
	policy   *Policy
	links    uint64
}

// FS is an in-memory filesystem namespace with fscrypt policy enforcement.
//
// Policies are attached to directories and inherited by newly created
// children. All operations are serialized, which makes each policy check and
// the following tree mutation one atomic step.
type FS struct {
	mu     sync.RWMutex
	root   *node
	nextID NodeID
}

// NewFS returns an empty filesystem with an unencrypted root directory.
func NewFS() *FS {
	fs := &FS{nextID: 1}
	fs.root = &node{
		id:       fs.nextID,
		kind:     KindDir,
		children: make(map[string]*node),
		links:    1,
	}
	fs.nextID++
	return fs
}

// Mkdir creates an empty directory. It inherits the parent directory's policy
// exactly. An empty directory created below an unencrypted parent can then be
// encrypted with SetPolicy.
func (fs *FS) Mkdir(p string) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	parent, name, err := fs.resolveParent(parts)
	if err != nil {
		return err
	}
	if parent.children[name] != nil {
		return fmt.Errorf("%w: %s", ErrExist, p)
	}

	child := &node{
		id:       fs.nextID,
		kind:     KindDir,
		parent:   parent,
		children: make(map[string]*node),
		policy:   clonePolicy(parent.policy),
		links:    1,
	}
	fs.nextID++
	parent.children[name] = child
	return nil
}

// CreateFile creates an empty regular file. It inherits the parent directory's
// policy exactly.
func (fs *FS) CreateFile(p string) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	parent, name, err := fs.resolveParent(parts)
	if err != nil {
		return err
	}
	if parent.children[name] != nil {
		return fmt.Errorf("%w: %s", ErrExist, p)
	}

	child := &node{
		id:     fs.nextID,
		kind:   KindFile,
		policy: clonePolicy(parent.policy),
		links:  1,
	}
	fs.nextID++
	parent.children[name] = child
	return nil
}

// Create is an alias for CreateFile.
func (fs *FS) Create(p string) error {
	return fs.CreateFile(p)
}

// SetPolicy attaches policy to an empty directory that does not already have a
// policy. Policies are immutable once set, and a non-empty directory cannot be
// given a policy.
func (fs *FS) SetPolicy(p string, policy Policy) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	n, err := fs.resolve(parts)
	if err != nil {
		return err
	}
	if n.kind != KindDir {
		return fmt.Errorf("%w: %s", ErrNotDirectory, p)
	}
	if n.policy != nil {
		return fmt.Errorf("%w: %s", ErrPolicyAlreadySet, p)
	}
	if len(n.children) != 0 {
		return fmt.Errorf("%w: %s", ErrNotEmpty, p)
	}

	attached := policy
	n.policy = &attached
	return nil
}

// Stat returns a snapshot of the node at p.
func (fs *FS) Stat(p string) (Info, error) {
	parts, err := splitPath(p)
	if err != nil {
		return Info{}, err
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	n, err := fs.resolve(parts)
	if err != nil {
		return Info{}, err
	}
	info := Info{
		ID:        n.id,
		Kind:      n.kind,
		Encrypted: n.policy != nil,
		Links:     n.links,
	}
	if n.policy != nil {
		info.Policy = *n.policy
	}
	return info, nil
}

// ReadDir returns the entries of a directory sorted by name.
func (fs *FS) ReadDir(p string) ([]DirEntry, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, err
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	n, err := fs.resolve(parts)
	if err != nil {
		return nil, err
	}
	if n.kind != KindDir {
		return nil, fmt.Errorf("%w: %s", ErrNotDirectory, p)
	}

	entries := make([]DirEntry, 0, len(n.children))
	for name, child := range n.children {
		entries = append(entries, DirEntry{Name: name, Kind: child.kind, ID: child.id})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Rename moves oldPath to newPath. The old and new parent directories must
// have exactly the same policy. An existing destination entry is replaced
// using rename(2)-style rules.
func (fs *FS) Rename(oldPath, newPath string) error {
	oldParts, err := splitPath(oldPath)
	if err != nil {
		return err
	}
	newParts, err := splitPath(newPath)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	srcParent, srcName, err := fs.resolveParent(oldParts)
	if err != nil {
		return err
	}
	src := srcParent.children[srcName]
	if src == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, oldPath)
	}

	dstParent, dstName, err := fs.resolveParent(newParts)
	if err != nil {
		return err
	}
	if srcParent == dstParent && srcName == dstName {
		return nil
	}

	if src.kind == KindDir && isDescendant(dstParent, src) {
		return fmt.Errorf("%w: %s is inside %s", ErrInvalidMove, newPath, oldPath)
	}
	if err := checkPlacement(src, srcParent, dstParent); err != nil {
		return err
	}

	dst := dstParent.children[dstName]
	if dst == src {
		return nil
	}
	if dst != nil {
		switch {
		case src.kind == KindDir && dst.kind != KindDir:
			return fmt.Errorf("%w: %s", ErrNotDirectory, newPath)
		case src.kind != KindDir && dst.kind == KindDir:
			return fmt.Errorf("%w: %s", ErrIsDirectory, newPath)
		case dst.kind == KindDir && len(dst.children) != 0:
			return fmt.Errorf("%w: %s", ErrNotEmpty, newPath)
		case src.kind == KindDir && isDescendant(src, dst):
			return fmt.Errorf("%w: %s is inside %s", ErrInvalidMove, oldPath, newPath)
		}
	}

	delete(srcParent.children, srcName)
	if dst != nil {
		delete(dstParent.children, dstName)
		unlink(dst)
	}
	dstParent.children[dstName] = src
	if src.kind == KindDir {
		src.parent = dstParent
	}
	return nil
}

// Link creates a hard link at newPath to the regular file at oldPath. The
// source and destination parent directories must have exactly the same
// policy, and the file's own policy must match the destination.
func (fs *FS) Link(oldPath, newPath string) error {
	oldParts, err := splitPath(oldPath)
	if err != nil {
		return err
	}
	newParts, err := splitPath(newPath)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	srcParent, srcName, err := fs.resolveParent(oldParts)
	if err != nil {
		return err
	}
	src := srcParent.children[srcName]
	if src == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, oldPath)
	}
	if src.kind == KindDir {
		return fmt.Errorf("%w: %s", ErrIsDirectory, oldPath)
	}

	dstParent, dstName, err := fs.resolveParent(newParts)
	if err != nil {
		return err
	}
	if dstParent.children[dstName] != nil {
		return fmt.Errorf("%w: %s", ErrExist, newPath)
	}
	if err := checkPlacement(src, srcParent, dstParent); err != nil {
		return err
	}

	dstParent.children[dstName] = src
	src.links++
	return nil
}

// RenameExchange atomically swaps the entries at a and b, like renameat2 with
// RENAME_EXCHANGE. Both directions are policy-checked before the tree changes.
func (fs *FS) RenameExchange(a, b string) error {
	aParts, err := splitPath(a)
	if err != nil {
		return err
	}
	bParts, err := splitPath(b)
	if err != nil {
		return err
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	aParent, aName, err := fs.resolveParent(aParts)
	if err != nil {
		return err
	}
	first := aParent.children[aName]
	if first == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, a)
	}

	bParent, bName, err := fs.resolveParent(bParts)
	if err != nil {
		return err
	}
	second := bParent.children[bName]
	if second == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, b)
	}
	if first == second {
		return nil
	}

	if first.kind == KindDir && isDescendant(bParent, first) {
		return fmt.Errorf("%w: %s is inside %s", ErrInvalidMove, b, a)
	}
	if second.kind == KindDir && isDescendant(aParent, second) {
		return fmt.Errorf("%w: %s is inside %s", ErrInvalidMove, a, b)
	}
	if err := checkPlacement(first, aParent, bParent); err != nil {
		return err
	}
	if err := checkPlacement(second, bParent, aParent); err != nil {
		return err
	}

	aParent.children[aName] = second
	bParent.children[bName] = first
	if first.kind == KindDir {
		first.parent = bParent
	}
	if second.kind == KindDir {
		second.parent = aParent
	}
	return nil
}

// Exchange is an alias for RenameExchange.
func (fs *FS) Exchange(a, b string) error {
	return fs.RenameExchange(a, b)
}

// Remove deletes a directory entry. Directories must be empty to be removed.
func (fs *FS) Remove(p string) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	parent, name, err := fs.resolveParent(parts)
	if err != nil {
		return err
	}
	n := parent.children[name]
	if n == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, p)
	}
	if n.kind == KindDir && len(n.children) != 0 {
		return fmt.Errorf("%w: %s", ErrNotEmpty, p)
	}

	delete(parent.children, name)
	unlink(n)
	return nil
}

func (fs *FS) resolve(parts []string) (*node, error) {
	n := fs.root
	for _, name := range parts {
		if n.kind != KindDir {
			return nil, ErrNotDirectory
		}
		child := n.children[name]
		if child == nil {
			return nil, ErrNotExist
		}
		n = child
	}
	return n, nil
}

func (fs *FS) resolveParent(parts []string) (*node, string, error) {
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("%w: the root has no parent", ErrInvalidMove)
	}

	n := fs.root
	for _, name := range parts[:len(parts)-1] {
		if n.kind != KindDir {
			return nil, "", ErrNotDirectory
		}
		child := n.children[name]
		if child == nil {
			return nil, "", ErrNotExist
		}
		n = child
	}
	if n.kind != KindDir {
		return nil, "", ErrNotDirectory
	}
	return n, parts[len(parts)-1], nil
}

func splitPath(p string) ([]string, error) {
	if p == "" || strings.IndexByte(p, 0) >= 0 {
		return nil, fmt.Errorf("%w: empty path or NUL byte", ErrInvalidPath)
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = pathpkg.Clean(p)
	if p == "/" {
		return nil, nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, name := range parts {
		if !validName(name) {
			return nil, fmt.Errorf("%w: invalid name %q", ErrInvalidPath, name)
		}
	}
	return parts, nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsRune(name, '/') && !strings.ContainsRune(name, 0)
}

func isDescendant(n, ancestor *node) bool {
	for current := n; current != nil; current = current.parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

func unlink(n *node) {
	if n.kind == KindFile && n.links > 0 {
		n.links--
	}
}

// checkPlacement enforces the policy boundary for moving item from srcDir to
// dstDir.
//
// A regular file carries the policy it inherited when it was created. A
// directory can be a policy root when its own policy differs from its
// (plaintext) parent; such a root may be moved between plaintext directories,
// but it may not be placed below an encrypted parent with a different policy.
func checkPlacement(item, srcDir, dstDir *node) error {
	itemEncrypted := item.policy != nil
	dstEncrypted := dstDir.policy != nil

	if dstEncrypted && !itemEncrypted {
		return fmt.Errorf("%w: item has no policy", ErrPlaintextIntoEncrypted)
	}
	if !dstEncrypted {
		switch {
		case item.kind == KindFile && itemEncrypted:
			return fmt.Errorf("%w: file has a policy", ErrEncryptedIntoPlaintext)
		case item.kind == KindDir && itemEncrypted && srcDir.policy != nil:
			return fmt.Errorf("%w: directory is encrypted", ErrEncryptedIntoPlaintext)
		}
	}

	if !policiesEqual(srcDir.policy, dstDir.policy) {
		return fmt.Errorf("%w: source and destination directories differ", ErrPolicyMismatch)
	}

	if item.kind == KindFile {
		if !policiesEqual(item.policy, dstDir.policy) {
			return fmt.Errorf("%w: file policy differs from destination", ErrPolicyMismatch)
		}
	} else if dstEncrypted && !policiesEqual(item.policy, dstDir.policy) {
		return fmt.Errorf("%w: directory policy differs from destination", ErrPolicyMismatch)
	}
	return nil
}
