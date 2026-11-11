package manifestbind

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// FS is the minimal writable file system surface the installer needs.
// All paths are slash-separated and relative to the FS root.
type FS interface {
	MkdirAll(dir string) error
	Create(name string) (io.WriteCloser, error)
	RemoveAll(name string) error
	Rename(oldName, newName string) error
	Stat(name string) (fs.FileInfo, error)
}

// OsFS implements FS against the host operating system, rooted at dir.
type OsFS struct {
	Root string
}

func (o OsFS) full(name string) string {
	if o.Root == "" {
		return name
	}
	return o.Root + "/" + name
}

func (o OsFS) MkdirAll(dir string) error   { return os.MkdirAll(o.full(dir), 0o755) }
func (o OsFS) RemoveAll(name string) error { return os.RemoveAll(o.full(name)) }
func (o OsFS) Rename(oldName, newName string) error {
	return os.Rename(o.full(oldName), o.full(newName))
}
func (o OsFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(o.full(name)) }

func (o OsFS) Create(name string) (io.WriteCloser, error) {
	return os.OpenFile(o.full(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
}

// MemFS is an in-memory FS for tests and sandboxed installs.
type MemFS struct {
	dirs  map[string]bool
	files map[string][]byte
}

// NewMemFS returns an empty in-memory file system.
func NewMemFS() *MemFS {
	return &MemFS{
		dirs:  map[string]bool{".": true},
		files: make(map[string][]byte),
	}
}

func (m *MemFS) MkdirAll(dir string) error {
	dir = path.Clean(dir)
	for i := 0; i <= len(dir); i++ {
		if i == len(dir) || dir[i] == '/' {
			m.dirs[dir[:i]] = true
		}
	}
	return nil
}

func (m *MemFS) Create(name string) (io.WriteCloser, error) {
	name = path.Clean(name)
	if !m.dirs[path.Dir(name)] {
		return nil, fs.ErrNotExist
	}
	if m.dirs[name] {
		return nil, errors.New("manifestbind: is a directory: " + name)
	}
	if _, ok := m.files[name]; ok {
		return nil, fs.ErrExist
	}
	return &memFile{fsys: m, name: name}, nil
}

func (m *MemFS) RemoveAll(name string) error {
	name = path.Clean(name)
	prefix := name + "/"
	for p := range m.files {
		if p == name || strings.HasPrefix(p, prefix) {
			delete(m.files, p)
		}
	}
	for d := range m.dirs {
		if d != "." && (d == name || strings.HasPrefix(d, prefix)) {
			delete(m.dirs, d)
		}
	}
	return nil
}

func (m *MemFS) Rename(oldName, newName string) error {
	oldName = path.Clean(oldName)
	newName = path.Clean(newName)
	if _, ok := m.files[oldName]; !ok && !m.dirs[oldName] {
		return fs.ErrNotExist
	}
	if !m.dirs[path.Dir(newName)] {
		return fs.ErrNotExist
	}
	m.RemoveAll(newName)
	oldPrefix := oldName + "/"
	for _, p := range m.sortedFiles() {
		if p == oldName || strings.HasPrefix(p, oldPrefix) {
			m.files[newName+strings.TrimPrefix(p, oldName)] = m.files[p]
			delete(m.files, p)
		}
	}
	var dirs []string
	for d := range m.dirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if d == oldName || strings.HasPrefix(d, oldPrefix) {
			delete(m.dirs, d)
			m.MkdirAll(newName + strings.TrimPrefix(d, oldName))
		}
	}
	return nil
}

func (m *MemFS) Stat(name string) (fs.FileInfo, error) {
	name = path.Clean(name)
	if data, ok := m.files[name]; ok {
		return memInfo{name: path.Base(name), size: int64(len(data))}, nil
	}
	if m.dirs[name] {
		return memInfo{name: path.Base(name), dir: true}, nil
	}
	return nil, fs.ErrNotExist
}

// ReadFile returns a copy of the file contents, for assertions in tests.
func (m *MemFS) ReadFile(name string) ([]byte, error) {
	data, ok := m.files[path.Clean(name)]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (m *MemFS) sortedFiles() []string {
	out := make([]string, 0, len(m.files))
	for p := range m.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type memFile struct {
	fsys *MemFS
	name string
	buf  []byte
}

func (f *memFile) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	return len(p), nil
}

func (f *memFile) Close() error {
	f.fsys.files[f.name] = f.buf
	return nil
}

type memInfo struct {
	name string
	size int64
	dir  bool
}

func (i memInfo) Name() string { return i.name }
func (i memInfo) Size() int64  { return i.size }
func (i memInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i memInfo) ModTime() (t time.Time) { return }
func (i memInfo) IsDir() bool            { return i.dir }
func (i memInfo) Sys() any               { return nil }
