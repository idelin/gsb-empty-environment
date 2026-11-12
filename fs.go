package netsec

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound         = errors.New("netsec: path not found")
	ErrNotDir           = errors.New("netsec: not a directory")
	ErrIsDir            = errors.New("netsec: is a directory")
	ErrExist            = errors.New("netsec: entry already exists")
	ErrNotEmpty         = errors.New("netsec: directory not empty")
	ErrPolicyAlreadySet = errors.New("netsec: directory already has an encryption policy")
	ErrPolicyMismatch   = errors.New("netsec: source and target directories have different encryption policies")
)

// node is an entry in the in-memory filesystem tree. Regular files may be
// shared between directories via hard links, so names live in the parent's
// children map rather than on the node.
type node struct {
	isDir    bool
	policy   *Policy // nil means plaintext
	children map[string]*node
}

func newDir(parent *node) *node {
	return &node{isDir: true, policy: parent.policy.clone(), children: map[string]*node{}}
}

func newFile(parent *node) *node {
	return &node{policy: parent.policy.clone()}
}

// FS is an in-memory filesystem model enforcing fscrypt policy inheritance:
// new entries inherit their parent directory's policy, and rename/link
// operations require the source and target directories to carry identical
// policies so that plaintext can never enter an encrypted directory and
// ciphertext can never leak into a plaintext one.
type FS struct {
	root *node
}

func NewFS() *FS {
	return &FS{root: &node{isDir: true, children: map[string]*node{}}}
}

func splitPath(path string) ([]string, error) {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: empty path", ErrNotFound)
	}
	for _, p := range parts {
		if p == "." || p == ".." {
			return nil, fmt.Errorf("%w: %q", ErrNotFound, path)
		}
	}
	return parts, nil
}

// lookupParent resolves all but the last component of path.
func (fs *FS) lookupParent(path string) (parent *node, name string, err error) {
	parts, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	cur := fs.root
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur.children[p]
		if !ok {
			return nil, "", fmt.Errorf("%w: %q", ErrNotFound, path)
		}
		if !next.isDir {
			return nil, "", fmt.Errorf("%w: %q", ErrNotDir, p)
		}
		cur = next
	}
	return cur, parts[len(parts)-1], nil
}

func (fs *FS) lookup(path string) (*node, error) {
	parent, name, err := fs.lookupParent(path)
	if err != nil {
		return nil, err
	}
	n, ok := parent.children[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, path)
	}
	return n, nil
}

// Mkdir creates a directory. The new directory inherits its parent's policy.
func (fs *FS) Mkdir(path string) error {
	parent, name, err := fs.lookupParent(path)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return fmt.Errorf("%w: %q", ErrExist, path)
	}
	parent.children[name] = newDir(parent)
	return nil
}

// Create creates an empty regular file. The file inherits its parent
// directory's policy (version, master key identifier, contents and
// filenames encryption modes, and flags).
func (fs *FS) Create(path string) error {
	parent, name, err := fs.lookupParent(path)
	if err != nil {
		return err
	}
	if _, ok := parent.children[name]; ok {
		return fmt.Errorf("%w: %q", ErrExist, path)
	}
	parent.children[name] = newFile(parent)
	return nil
}

// SetPolicy assigns an encryption policy to a directory. The directory must
// be empty and must not already have a policy: fscrypt forbids both
// encrypting a non-empty directory and changing an existing policy.
func (fs *FS) SetPolicy(path string, p Policy) error {
	n, err := fs.lookup(path)
	if err != nil {
		return err
	}
	if !n.isDir {
		return fmt.Errorf("%w: %q", ErrNotDir, path)
	}
	if n.policy != nil {
		return fmt.Errorf("%w: %q", ErrPolicyAlreadySet, path)
	}
	if len(n.children) != 0 {
		return fmt.Errorf("%w: %q", ErrNotEmpty, path)
	}
	n.policy = p.clone()
	return nil
}

// GetPolicy returns the policy stored on the entry at path, or nil if the
// entry is plaintext.
func (fs *FS) GetPolicy(path string) (*Policy, error) {
	n, err := fs.lookup(path)
	if err != nil {
		return nil, err
	}
	return n.policy.clone(), nil
}

// checkMovePolicies enforces the core invariant: an entry may only be
// placed into a directory whose policy is identical to the policy of the
// directory it currently lives in. This blocks plaintext files from
// entering encrypted directories, ciphertext from entering plaintext
// directories, and files encrypted under one key/mode from entering a
// directory protected by another.
func checkMovePolicies(srcParent, dstParent *node) error {
	if !srcParent.policy.Equal(dstParent.policy) {
		return ErrPolicyMismatch
	}
	return nil
}

// Rename moves src to dst. The source and destination directories must have
// identical policies. An existing destination is replaced if it is a
// regular file or an empty directory of the same type.
func (fs *FS) Rename(src, dst string) error {
	srcParent, srcName, err := fs.lookupParent(src)
	if err != nil {
		return err
	}
	n, ok := srcParent.children[srcName]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, src)
	}
	dstParent, dstName, err := fs.lookupParent(dst)
	if err != nil {
		return err
	}
	if err := checkMovePolicies(srcParent, dstParent); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", src, dst, err)
	}
	if old, ok := dstParent.children[dstName]; ok {
		if old.isDir != n.isDir {
			return fmt.Errorf("%w: %q", ErrExist, dst)
		}
		if old.isDir && len(old.children) != 0 {
			return fmt.Errorf("%w: %q", ErrNotEmpty, dst)
		}
	}
	delete(srcParent.children, srcName)
	dstParent.children[dstName] = n
	return nil
}

// Exchange atomically swaps the names a and b (RENAME_EXCHANGE semantics).
// Both entries must exist and their parent directories must have identical
// policies.
func (fs *FS) Exchange(a, b string) error {
	aParent, aName, err := fs.lookupParent(a)
	if err != nil {
		return err
	}
	bParent, bName, err := fs.lookupParent(b)
	if err != nil {
		return err
	}
	aNode, ok := aParent.children[aName]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, a)
	}
	bNode, ok := bParent.children[bName]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, b)
	}
	if err := checkMovePolicies(aParent, bParent); err != nil {
		return fmt.Errorf("exchange %q <-> %q: %w", a, b, err)
	}
	aParent.children[aName], bParent.children[bName] = bNode, aNode
	return nil
}

// Link creates a hard link at dst referring to the regular file at src.
// The source and destination directories must have identical policies, so a
// plaintext file cannot be linked into an encrypted directory (or vice
// versa) to escape its policy.
func (fs *FS) Link(src, dst string) error {
	srcParent, srcName, err := fs.lookupParent(src)
	if err != nil {
		return err
	}
	n, ok := srcParent.children[srcName]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, src)
	}
	if n.isDir {
		return fmt.Errorf("%w: %q", ErrIsDir, src)
	}
	dstParent, dstName, err := fs.lookupParent(dst)
	if err != nil {
		return err
	}
	if err := checkMovePolicies(srcParent, dstParent); err != nil {
		return fmt.Errorf("link %q -> %q: %w", src, dst, err)
	}
	if _, ok := dstParent.children[dstName]; ok {
		return fmt.Errorf("%w: %q", ErrExist, dst)
	}
	dstParent.children[dstName] = n
	return nil
}
