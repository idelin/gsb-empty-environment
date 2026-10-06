package cleaner

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
)

type memNode struct {
	id      uint64
	version uint64
	isDir   bool
	data    []byte
}

// MemFS is an in-memory virtual filesystem. It implements FS so the cleaner
// can be exercised deterministically in tests, and it offers mutation
// helpers (WriteFile, Rename, FailRemove, ...) to script races and failures
// between planning and execution.
type MemFS struct {
	mu        sync.Mutex
	nodes     map[string]*memNode
	nextID    uint64
	removeErr map[string]error
}

// NewMemFS returns an empty virtual filesystem containing only "/".
func NewMemFS() *MemFS {
	m := &MemFS{
		nodes:     make(map[string]*memNode),
		nextID:    1,
		removeErr: make(map[string]error),
	}
	m.nodes["/"] = &memNode{id: m.allocID(), isDir: true}
	return m
}

func (m *MemFS) allocID() uint64 {
	id := m.nextID
	m.nextID++
	return id
}

func normalize(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// MkdirAll creates a directory and any missing parents.
func (m *MemFS) MkdirAll(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := "/"
	for _, part := range strings.Split(strings.Trim(normalize(p), "/"), "/") {
		if part == "" {
			continue
		}
		cur = path.Join(cur, part)
		if n, ok := m.nodes[cur]; ok {
			if !n.isDir {
				return &os.PathError{Op: "mkdir", Path: cur, Err: errors.New("not a directory")}
			}
			continue
		}
		m.nodes[cur] = &memNode{id: m.allocID(), isDir: true}
	}
	return nil
}

// WriteFile writes data to path, creating the file if needed. Writing to an
// existing file keeps its identity but bumps its version and size, mirroring
// an in-place truncate-and-write on a real filesystem.
func (m *MemFS) WriteFile(p string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p = normalize(p)
	parent, ok := m.nodes[path.Dir(p)]
	if !ok || !parent.isDir {
		return &os.PathError{Op: "write", Path: p, Err: fs.ErrNotExist}
	}
	if n, ok := m.nodes[p]; ok {
		if n.isDir {
			return &os.PathError{Op: "write", Path: p, Err: errors.New("is a directory")}
		}
		n.version++
		n.data = append([]byte(nil), data...)
		return nil
	}
	m.nodes[p] = &memNode{id: m.allocID(), version: 1, data: append([]byte(nil), data...)}
	return nil
}

// ReadFile returns the content of the file at path.
func (m *MemFS) ReadFile(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[normalize(p)]
	if !ok {
		return nil, &os.PathError{Op: "read", Path: p, Err: fs.ErrNotExist}
	}
	if n.isDir {
		return nil, &os.PathError{Op: "read", Path: p, Err: errors.New("is a directory")}
	}
	return append([]byte(nil), n.data...), nil
}

// Rename moves oldPath to newPath, preserving identity and version. Moving a
// directory moves its whole subtree. An existing destination is replaced.
func (m *MemFS) Rename(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldPath, newPath = normalize(oldPath), normalize(newPath)
	if _, ok := m.nodes[oldPath]; !ok {
		return &os.PathError{Op: "rename", Path: oldPath, Err: fs.ErrNotExist}
	}
	parent, ok := m.nodes[path.Dir(newPath)]
	if !ok || !parent.isDir {
		return &os.PathError{Op: "rename", Path: newPath, Err: fs.ErrNotExist}
	}
	moves := make(map[string]string)
	for k := range m.nodes {
		if k == oldPath || strings.HasPrefix(k, oldPath+"/") {
			moves[k] = newPath + strings.TrimPrefix(k, oldPath)
		}
	}
	for k := range m.nodes {
		for _, dst := range moves {
			if k == dst || strings.HasPrefix(k, dst+"/") {
				delete(m.nodes, k)
			}
		}
	}
	for src, dst := range moves {
		m.nodes[dst] = m.nodes[src]
		delete(m.nodes, src)
	}
	return nil
}

// FailRemove arranges for Remove(path) to fail with err, simulating a
// partial deletion failure.
func (m *MemFS) FailRemove(p string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeErr[normalize(p)] = err
}

// Stat implements FS.
func (m *MemFS) Stat(p string) (FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[normalize(p)]
	if !ok {
		return FileInfo{}, &os.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	return FileInfo{ID: n.id, Version: n.version, Size: int64(len(n.data)), IsDir: n.isDir}, nil
}

// ReadDir implements FS.
func (m *MemFS) ReadDir(p string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p = normalize(p)
	n, ok := m.nodes[p]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
	}
	if !n.isDir {
		return nil, &os.PathError{Op: "readdir", Path: p, Err: errors.New("not a directory")}
	}
	var names []string
	for k := range m.nodes {
		if k != p && path.Dir(k) == p {
			names = append(names, path.Base(k))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Remove implements FS. Directories must be empty to be removed.
func (m *MemFS) Remove(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p = normalize(p)
	if err, ok := m.removeErr[p]; ok {
		return err
	}
	n, ok := m.nodes[p]
	if !ok {
		return &os.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
	}
	if n.isDir {
		if p == "/" {
			return &os.PathError{Op: "remove", Path: p, Err: errors.New("cannot remove root")}
		}
		for k := range m.nodes {
			if path.Dir(k) == p && k != p {
				return &os.PathError{Op: "remove", Path: p, Err: errors.New("directory not empty")}
			}
		}
	}
	delete(m.nodes, p)
	return nil
}

// Join implements FS.
func (m *MemFS) Join(elem ...string) string { return path.Join(elem...) }
