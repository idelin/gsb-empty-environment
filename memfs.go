package cleaner

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// MemFS is an in-memory virtual filesystem used to test the cleaner. Every
// node gets a unique inode; every mutation bumps a Lamport clock so modtime
// always advances, mirroring real filesystem version semantics.
type MemFS struct {
	root    *memNode
	nextIno uint64
	clock   int64
	FailOps map[string]error // path -> injected removal error
}

type memNode struct {
	ino      uint64
	name     string
	isDir    bool
	data     []byte
	modNano  int64
	children map[string]*memNode
	parent   *memNode
}

func NewMemFS() *MemFS {
	m := &MemFS{nextIno: 1}
	m.root = &memNode{ino: m.alloc(), name: "/", isDir: true, children: map[string]*memNode{}}
	m.root.modNano = m.tick()
	return m
}

func (m *MemFS) alloc() uint64 { ino := m.nextIno; m.nextIno++; return ino }

func (m *MemFS) tick() int64 { m.clock++; return m.clock }

func split(path string) []string {
	clean := strings.Trim(path, "/")
	if clean == "" {
		return nil
	}
	return strings.Split(clean, "/")
}

func (m *MemFS) lookup(path string) (*memNode, error) {
	node := m.root
	for _, part := range split(path) {
		if !node.isDir {
			return nil, fmt.Errorf("%s: not a directory", path)
		}
		child, ok := node.children[part]
		if !ok {
			return nil, fs.ErrNotExist
		}
		node = child
	}
	return node, nil
}

func (m *MemFS) parentOf(path string) (*memNode, string, error) {
	parts := split(path)
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("no parent for root")
	}
	parent, err := m.lookup("/" + strings.Join(parts[:len(parts)-1], "/"))
	if err != nil {
		return nil, "", err
	}
	return parent, parts[len(parts)-1], nil
}

// MkdirAll creates a directory and any missing parents.
func (m *MemFS) MkdirAll(path string) {
	node := m.root
	for _, part := range split(path) {
		child, ok := node.children[part]
		if !ok {
			child = &memNode{
				ino: m.alloc(), name: part, isDir: true,
				children: map[string]*memNode{}, parent: node,
			}
			child.modNano = m.tick()
			node.children[part] = child
			node.modNano = m.tick()
		}
		node = child
	}
}

// WriteFile creates or overwrites a file in place: the inode is kept but
// version (modtime) and size change, like a truncate-and-rewrite.
func (m *MemFS) WriteFile(path string, data []byte) {
	if node, err := m.lookup(path); err == nil && !node.isDir {
		node.data = append(node.data[:0], data...)
		node.modNano = m.tick()
		return
	}
	parent, name, err := m.parentOf(path)
	if err != nil {
		panic(err)
	}
	node := &memNode{ino: m.alloc(), name: name, data: append([]byte(nil), data...), parent: parent}
	node.modNano = m.tick()
	parent.children[name] = node
	parent.modNano = m.tick()
}

// ReplaceFile atomically swaps a new file into path with a fresh inode, like
// write-temp-then-rename.
func (m *MemFS) ReplaceFile(path string, data []byte) {
	parent, name, err := m.parentOf(path)
	if err != nil {
		panic(err)
	}
	node := &memNode{ino: m.alloc(), name: name, data: append([]byte(nil), data...), parent: parent}
	node.modNano = m.tick()
	parent.children[name] = node
	parent.modNano = m.tick()
}

// Move renames a path, preserving the inode of the moved node.
func (m *MemFS) Move(oldPath, newPath string) {
	node, err := m.lookup(oldPath)
	if err != nil {
		panic(err)
	}
	oldParent, oldName, err := m.parentOf(oldPath)
	if err != nil {
		panic(err)
	}
	newParent, newName, err := m.parentOf(newPath)
	if err != nil {
		panic(err)
	}
	delete(oldParent.children, oldName)
	oldParent.modNano = m.tick()
	node.name = newName
	node.parent = newParent
	newParent.children[newName] = node
	newParent.modNano = m.tick()
}

// Read returns a file's contents.
func (m *MemFS) Read(path string) ([]byte, error) {
	node, err := m.lookup(path)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), node.data...), nil
}

// Exists reports whether path exists.
func (m *MemFS) Exists(path string) bool {
	_, err := m.lookup(path)
	return err == nil
}

func (m *MemFS) Stat(path string) (FileInfo, error) {
	node, err := m.lookup(path)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{
		ID:      FileID{Dev: 1, Ino: node.ino},
		Size:    int64(len(node.data)),
		ModTime: time.Unix(0, node.modNano).UTC(),
		IsDir:   node.isDir,
	}, nil
}

func (m *MemFS) ReadDir(path string) ([]string, error) {
	node, err := m.lookup(path)
	if err != nil {
		return nil, err
	}
	if !node.isDir {
		return nil, fmt.Errorf("%s: not a directory", path)
	}
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (m *MemFS) RemoveFile(path string) error { return m.remove(path, false) }

func (m *MemFS) RemoveDir(path string) error { return m.remove(path, true) }

func (m *MemFS) remove(path string, wantDir bool) error {
	if m.FailOps != nil {
		if err, ok := m.FailOps[path]; ok {
			return err
		}
	}
	node, err := m.lookup(path)
	if err != nil {
		return err
	}
	if node.isDir != wantDir {
		return fmt.Errorf("%s: wrong type for removal", path)
	}
	if node.isDir && len(node.children) > 0 {
		return fmt.Errorf("%s: directory not empty", path)
	}
	parent, name, err := m.parentOf(path)
	if err != nil {
		return err
	}
	delete(parent.children, name)
	parent.modNano = m.tick()
	return nil
}
