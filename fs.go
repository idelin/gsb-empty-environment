package netsec

import (
	"io/fs"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"
)

// FileSystem is the caller-injected filesystem used by Sandbox to
// resolve symbolic links and ".." segments during access checks.
// Lstat must not follow symbolic links; Readlink returns the raw
// target of a symlink.
type FileSystem interface {
	Lstat(path string) (fs.FileInfo, error)
	Readlink(path string) (string, error)
}

// OSFS adapts the real operating system filesystem to FileSystem.
type OSFS struct{}

func (OSFS) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (OSFS) Readlink(path string) (string, error)   { return os.Readlink(path) }

// MemFS is an in-memory FileSystem supporting directories, regular
// files and symbolic links. It is useful for tests and for modelling a
// virtual view of a filesystem. All paths are normalized with the same
// platform rules the Sandbox applies.
type MemFS struct {
	mu    sync.RWMutex
	nodes map[string]memNode
}

type memNode struct {
	mode   fs.FileMode
	target string // symlink target, only when mode has ModeSymlink set
}

// NewMemFS returns an empty MemFS containing only the root directory.
func NewMemFS() *MemFS {
	return &MemFS{nodes: map[string]memNode{
		"/": {mode: fs.ModeDir | 0o755},
	}}
}

// MkdirAll creates dir and any missing parents.
func (m *MemFS) MkdirAll(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirAllLocked(normalizePath(dir))
}

func (m *MemFS) mkdirAllLocked(dir string) {
	if dir == "" {
		dir = "/"
	}
	if _, ok := m.nodes[dir]; ok {
		return
	}
	m.mkdirAllLocked(parentOf(dir))
	m.nodes[dir] = memNode{mode: fs.ModeDir | 0o755}
}

// AddFile registers a regular file, creating missing parents.
func (m *MemFS) AddFile(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := normalizePath(path)
	m.mkdirAllLocked(parentOf(p))
	m.nodes[p] = memNode{mode: 0o644}
}

// Symlink creates a symbolic link at link pointing to target, creating
// missing parent directories. The target is stored verbatim and may be
// relative or absolute.
func (m *MemFS) Symlink(target, link string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := normalizePath(link)
	m.mkdirAllLocked(parentOf(l))
	m.nodes[l] = memNode{mode: fs.ModeSymlink | 0o777, target: target}
}

func (m *MemFS) Lstat(path string) (fs.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := normalizePath(path)
	n, ok := m.nodes[p]
	if !ok {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: syscall.ENOENT}
	}
	return memInfo{name: baseOf(p), mode: n.mode}, nil
}

func (m *MemFS) Readlink(path string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := normalizePath(path)
	n, ok := m.nodes[p]
	if !ok {
		return "", &fs.PathError{Op: "readlink", Path: path, Err: syscall.ENOENT}
	}
	if n.mode&fs.ModeSymlink == 0 {
		return "", &fs.PathError{Op: "readlink", Path: path, Err: syscall.EINVAL}
	}
	return n.target, nil
}

// Paths returns the sorted list of registered paths, for inspection.
func (m *MemFS) Paths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.nodes))
	for p := range m.nodes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type memInfo struct {
	name string
	mode fs.FileMode
}

func (i memInfo) Name() string {
	if i.name == "" {
		return "/"
	}
	return i.name
}
func (i memInfo) Size() int64        { return 0 }
func (i memInfo) Mode() fs.FileMode  { return i.mode }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return i.mode.IsDir() }
func (i memInfo) Sys() any           { return nil }
