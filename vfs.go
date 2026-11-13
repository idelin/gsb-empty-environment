package netsec

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type nodeKind uint8

const (
	nodeDirectory nodeKind = iota
	nodeFile
	nodeSymlink
)

type vnode struct {
	kind     nodeKind
	data     []byte
	target   string
	children map[string]*vnode
}

func newDirectory() *vnode {
	return &vnode{kind: nodeDirectory, children: make(map[string]*vnode)}
}

// VirtualFS holds immutable package source roots and isolated staging output
// roots. It is an in-memory model of directory handles: callback code never
// receives a host path or a raw directory descriptor.
type VirtualFS struct {
	mu             sync.RWMutex
	sources        map[Digest]*vnode
	outputs        map[Digest]*vnode
	outputFixtures map[Digest][]outputFixture
	frozen         bool
}

type outputFixture struct {
	path   string
	target string
}

// NewVirtualFS returns an empty virtual filesystem.
func NewVirtualFS() *VirtualFS {
	return &VirtualFS{
		sources:        make(map[Digest]*vnode),
		outputs:        make(map[Digest]*vnode),
		outputFixtures: make(map[Digest][]outputFixture),
	}
}

// AddSourceDir adds a directory below a package's immutable source root.
// Missing intermediate directories are created as real directories.
func (v *VirtualFS) AddSourceDir(digest Digest, path string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	root, err := v.ensureSourceLocked(digest)
	if err != nil {
		return err
	}
	_, err = ensureSetupNode(root, path, nodeDirectory, nil, "")
	return err
}

// AddSourceFile adds a regular file below a package's immutable source root.
func (v *VirtualFS) AddSourceFile(digest Digest, path string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	root, err := v.ensureSourceLocked(digest)
	if err != nil {
		return err
	}
	_, err = ensureSetupNode(root, path, nodeFile, data, "")
	return err
}

// AddSourceSymlink is primarily a hostile-fixture helper. The target may be an
// absolute host path or a parent traversal; resolution never follows it.
func (v *VirtualFS) AddSourceSymlink(digest Digest, path, target string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	if target == "" {
		return fmt.Errorf("netsec: empty symlink target")
	}
	if strings.ContainsRune(target, '\x00') {
		return fmt.Errorf("netsec: symlink target contains NUL")
	}
	root, err := v.ensureSourceLocked(digest)
	if err != nil {
		return err
	}
	_, err = ensureSetupNode(root, path, nodeSymlink, nil, target)
	return err
}

// AddOutputDir adds a directory to a package's isolated staging output root.
func (v *VirtualFS) AddOutputDir(digest Digest, path string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	root, err := v.ensureOutputLocked(digest)
	if err != nil {
		return err
	}
	_, err = ensureSetupNode(root, path, nodeDirectory, nil, "")
	return err
}

// AddOutputFile seeds a regular file in a package's staging root.
func (v *VirtualFS) AddOutputFile(digest Digest, path string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	root, err := v.ensureOutputLocked(digest)
	if err != nil {
		return err
	}
	_, err = ensureSetupNode(root, path, nodeFile, data, "")
	return err
}

// AddOutputSymlink is a hostile-fixture helper for write-escape tests. The
// fixture survives ResetOutput because the five script action forms cannot
// create symlinks.
func (v *VirtualFS) AddOutputSymlink(digest Digest, path, target string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	if target == "" {
		return fmt.Errorf("netsec: empty symlink target")
	}
	if strings.ContainsRune(target, '\x00') {
		return fmt.Errorf("netsec: symlink target contains NUL")
	}
	root, err := v.ensureOutputLocked(digest)
	if err != nil {
		return err
	}
	if _, err = ensureSetupNode(root, path, nodeSymlink, nil, target); err != nil {
		return err
	}
	if v.outputFixtures == nil {
		v.outputFixtures = make(map[Digest][]outputFixture)
	}
	v.outputFixtures[digest] = append(v.outputFixtures[digest], outputFixture{path: path, target: target})
	return err
}

// SourceFiles returns a defensive copy of all regular files in a source root.
func (v *VirtualFS) SourceFiles(digest Digest) map[string][]byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	root := v.sources[digest]
	return snapshotRegularFiles(root)
}

// OutputFiles returns a defensive copy of all regular files in a staging root.
func (v *VirtualFS) OutputFiles(digest Digest) map[string][]byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	root := v.outputs[digest]
	return snapshotRegularFiles(root)
}

// ResetOutput discards a package's staged output root.
func (v *VirtualFS) ResetOutput(digest Digest) error {
	if err := digest.Validate(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return fmt.Errorf("netsec: source and output roots are sealed")
	}
	v.resetOutputLocked(digest)
	return nil
}

func (v *VirtualFS) resetOutput(digest Digest) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.resetOutputLocked(digest)
}

func (v *VirtualFS) resetOutputLocked(digest Digest) {
	if v.outputs == nil {
		v.outputs = make(map[Digest]*vnode)
	}
	v.outputs[digest] = newDirectory()
	for _, fixture := range v.outputFixtures[digest] {
		_, _ = ensureSetupNode(v.outputs[digest], fixture.path, nodeSymlink, nil, fixture.target)
	}
}

func (v *VirtualFS) seal() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.frozen = true
}

func (v *VirtualFS) unseal() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.frozen = false
}

func (v *VirtualFS) ensureSourceRoot(digest Digest) error {
	if err := digest.Validate(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	_, err := v.ensureSourceLocked(digest)
	return err
}

func (v *VirtualFS) ensureSourceLocked(digest Digest) (*vnode, error) {
	if err := digest.Validate(); err != nil {
		return nil, err
	}
	if v.sources == nil {
		v.sources = make(map[Digest]*vnode)
	}
	root := v.sources[digest]
	if root == nil {
		root = newDirectory()
		v.sources[digest] = root
	}
	return root, nil
}

func (v *VirtualFS) ensureOutputLocked(digest Digest) (*vnode, error) {
	if err := digest.Validate(); err != nil {
		return nil, err
	}
	if v.outputs == nil {
		v.outputs = make(map[Digest]*vnode)
	}
	root := v.outputs[digest]
	if root == nil {
		root = newDirectory()
		v.outputs[digest] = root
	}
	return root, nil
}

func (v *VirtualFS) readSource(digest Digest, path string) ([]byte, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	root := v.sources[digest]
	if root == nil {
		return nil, &PathError{Code: PathMissingParent, Path: path, Component: ".", Message: "package source root is absent"}
	}
	components := strings.Split(path, "/")
	parent, err := walkParents(root, path, components)
	if err != nil {
		return nil, err
	}
	final, exists := parent.children[components[len(components)-1]]
	if !exists {
		return nil, &PathError{Code: PathNotFile, Path: path, Component: components[len(components)-1], Message: "file does not exist"}
	}
	switch final.kind {
	case nodeSymlink:
		return nil, &PathError{Code: PathSymlinkTarget, Path: path, Component: components[len(components)-1], Message: "symlink targets are never followed"}
	case nodeDirectory:
		return nil, &PathError{Code: PathNotFile, Path: path, Component: components[len(components)-1], Message: "path is a directory"}
	default:
		return append([]byte(nil), final.data...), nil
	}
}

func (v *VirtualFS) writeOutput(digest Digest, path string, data []byte) error {
	if err := validatePath(path); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	root, err := v.ensureOutputLocked(digest)
	if err != nil {
		return err
	}
	components := strings.Split(path, "/")
	parent, err := walkWriteParents(root, path, components)
	if err != nil {
		return err
	}
	name := components[len(components)-1]
	if final, exists := parent.children[name]; exists {
		switch final.kind {
		case nodeSymlink:
			return &PathError{Code: PathSymlinkTarget, Path: path, Component: name, Message: "symlink targets are never written"}
		case nodeDirectory:
			return &PathError{Code: PathDestination, Path: path, Component: name, Message: "destination is a directory"}
		}
		final.data = append(final.data[:0], data...)
		return nil
	}
	parent.children[name] = &vnode{kind: nodeFile, data: append([]byte(nil), data...)}
	return nil
}

func walkWriteParents(root *vnode, path string, components []string) (*vnode, error) {
	current := root
	for _, component := range components[:len(components)-1] {
		next, exists := current.children[component]
		if !exists {
			next = newDirectory()
			current.children[component] = next
		}
		switch next.kind {
		case nodeSymlink:
			return nil, &PathError{Code: PathSymlinkParent, Path: path, Component: component, Message: "symlink parents are never traversed"}
		case nodeFile:
			return nil, &PathError{Code: PathNotDirectory, Path: path, Component: component, Message: "parent component is a file"}
		}
		current = next
	}
	return current, nil
}

func walkParents(root *vnode, path string, components []string) (*vnode, error) {
	current := root
	for _, component := range components[:len(components)-1] {
		next, exists := current.children[component]
		if !exists {
			return nil, &PathError{Code: PathMissingParent, Path: path, Component: component, Message: "parent directory does not exist"}
		}
		switch next.kind {
		case nodeSymlink:
			return nil, &PathError{Code: PathSymlinkParent, Path: path, Component: component, Message: "symlink parents are never traversed"}
		case nodeFile:
			return nil, &PathError{Code: PathNotDirectory, Path: path, Component: component, Message: "parent component is a file"}
		}
		current = next
	}
	return current, nil
}

func ensureSetupNode(root *vnode, path string, kind nodeKind, data []byte, target string) (*vnode, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	components := strings.Split(path, "/")
	current := root
	for _, component := range components[:len(components)-1] {
		next, exists := current.children[component]
		if !exists {
			next = newDirectory()
			current.children[component] = next
		}
		switch next.kind {
		case nodeSymlink:
			return nil, &PathError{Code: PathSymlinkParent, Path: path, Component: component, Message: "cannot add below a symlink"}
		case nodeFile:
			return nil, &PathError{Code: PathNotDirectory, Path: path, Component: component, Message: "parent component is a file"}
		}
		current = next
	}
	name := components[len(components)-1]
	if existing, exists := current.children[name]; exists {
		if existing.kind != kind {
			return nil, &PathError{Code: PathDestination, Path: path, Component: name, Message: "existing entry has a different type"}
		}
		if kind == nodeDirectory {
			return existing, nil
		}
	}
	entry := &vnode{kind: kind}
	switch kind {
	case nodeFile:
		entry.data = append([]byte(nil), data...)
	case nodeSymlink:
		entry.target = target
	case nodeDirectory:
		entry.children = make(map[string]*vnode)
	}
	current.children[name] = entry
	return entry, nil
}

func snapshotRegularFiles(root *vnode) map[string][]byte {
	result := make(map[string][]byte)
	if root == nil {
		return result
	}
	var walk func(prefix string, node *vnode)
	walk = func(prefix string, node *vnode) {
		names := make([]string, 0, len(node.children))
		for name := range node.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := node.children[name]
			path := name
			if prefix != "" {
				path = prefix + "/" + name
			}
			switch child.kind {
			case nodeDirectory:
				walk(path, child)
			case nodeFile:
				result[path] = append([]byte(nil), child.data...)
			}
		}
	}
	walk("", root)
	return result
}
