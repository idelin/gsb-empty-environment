package publisher

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"sync"
)

// File is an open file handle. Sync flushes contents to durable storage.
type File interface {
	io.Writer
	Sync() error
	Close() error
}

// FS abstracts every durable-storage operation the publisher relies on.
// Implementations must provide atomic Rename (atomic within a directory).
type FS interface {
	MkdirAll(dir string) error
	Create(p string) (File, error)
	Open(p string) (io.ReadCloser, error)
	Rename(oldPath, newPath string) error
	Remove(p string) error // idempotent: removing a missing file succeeds
	SyncDir(dir string) error
	Exists(p string) bool
	ReadDir(dir string) ([]string, error)
}

// ---------------------------------------------------------------------------
// MemFS: in-memory FS that models durability. File contents become durable on
// File.Sync; directory entries (creations, renames, removals) become durable
// on SyncDir of the containing directory. Crash reverts to the durable state.
// ---------------------------------------------------------------------------

type memFile struct {
	data   []byte // volatile contents
	synced []byte // contents as of last File.Sync
}

type MemFS struct {
	mu      sync.Mutex
	files   map[string]*memFile // volatile namespace
	durable map[string][]byte   // durable namespace: path -> contents
}

func NewMemFS() *MemFS {
	return &MemFS{
		files:   map[string]*memFile{},
		durable: map[string][]byte{},
	}
}

func (m *MemFS) MkdirAll(dir string) error { return nil }

type memHandle struct {
	fs   *MemFS
	path string
}

func (m *MemFS) Create(p string) (File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[p] = &memFile{}
	return &memHandle{fs: m, path: p}, nil
}

func (h *memHandle) Write(b []byte) (int, error) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	f, ok := h.fs.files[h.path]
	if !ok {
		return 0, fmt.Errorf("write to unlinked file %q", h.path)
	}
	f.data = append(f.data, b...)
	return len(b), nil
}

func (h *memHandle) Sync() error {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	f, ok := h.fs.files[h.path]
	if !ok {
		return fmt.Errorf("sync of unlinked file %q", h.path)
	}
	f.synced = append([]byte(nil), f.data...)
	return nil
}

func (h *memHandle) Close() error { return nil }

func (m *MemFS) Open(p string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[p]
	if !ok {
		return nil, fmt.Errorf("open %q: not found", p)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), f.data...))), nil
}

func (m *MemFS) Rename(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[oldPath]
	if !ok {
		return fmt.Errorf("rename %q: not found", oldPath)
	}
	delete(m.files, oldPath)
	m.files[newPath] = f
	return nil
}

func (m *MemFS) Remove(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, p)
	return nil
}

// SyncDir reconciles the durable namespace with the volatile one for all
// entries directly inside dir: entries present in the volatile namespace are
// persisted with their last-synced contents, entries absent are dropped.
func (m *MemFS) SyncDir(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, f := range m.files {
		if path.Dir(p) == dir {
			m.durable[p] = append([]byte(nil), f.synced...)
		}
	}
	for p := range m.durable {
		if path.Dir(p) == dir {
			if _, ok := m.files[p]; !ok {
				delete(m.durable, p)
			}
		}
	}
	return nil
}

func (m *MemFS) Exists(p string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[p]
	return ok
}

func (m *MemFS) ReadDir(dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for p := range m.files {
		if path.Dir(p) == dir {
			names = append(names, path.Base(p))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Crash simulates a power loss: the volatile namespace is discarded and
// replaced by the durable state.
func (m *MemFS) Crash() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files = map[string]*memFile{}
	for p, d := range m.durable {
		cp := append([]byte(nil), d...)
		m.files[p] = &memFile{data: cp, synced: cp}
	}
}

// ---------------------------------------------------------------------------
// OSFS: real filesystem implementation for production use.
// ---------------------------------------------------------------------------

type OSFS struct{}

func (OSFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

func (OSFS) Create(p string) (File, error) { return os.Create(p) }

func (OSFS) Open(p string) (io.ReadCloser, error) { return os.Open(p) }

func (OSFS) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

func (OSFS) Remove(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (OSFS) SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (OSFS) Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (OSFS) ReadDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
