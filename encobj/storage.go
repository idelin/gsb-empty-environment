package encobj

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Storage abstracts every file operation the store performs. It is
// injectable so tests can substitute in-memory, fault-injecting or
// scheduling-controlled implementations.
//
// Required semantics (see FORMAT.md):
//   - PutAtomic must be an atomic, durable replace: write a temp file in the
//     same directory, fsync it, rename over the target, then fsync the
//     containing directory. After PutAtomic returns, a crash must observe
//     either the old or the new contents, never a partial file.
//   - Append followed by Sync must make the appended bytes durable. Bytes
//     appended but never Synced may be lost or torn on crash; the journal
//     recovery detects and truncates such tails.
//   - Rename (inside PutAtomic) must be atomic within the same directory.
//   - SyncDir must fsync a directory so that creations/renames/removals of
//     its children are durable.
type Storage interface {
	ReadAll(name string) ([]byte, error)
	PutAtomic(name string, data []byte) error
	Append(name string, data []byte) error
	Sync(name string) error
	Truncate(name string, size int64) error
	Size(name string) (int64, error)
	List(dir string) ([]string, error)
	Remove(name string) error
	RemoveAll(dir string) error
	MkdirAll(dir string) error
	Exists(name string) bool
	CreateExcl(name string, data []byte) error
	SyncDir(dir string) error
}

// ---------------------------------------------------------------------------
// dirStorage: real filesystem rooted at a directory.
// ---------------------------------------------------------------------------

type dirStorage struct{ root string }

// NewDirStorage returns a Storage backed by the filesystem directory root.
func NewDirStorage(root string) Storage { return dirStorage{root: root} }

func (d dirStorage) full(name string) string {
	clean := path.Join("/", name) // neutralises ".." and absolute paths
	return filepath.Join(d.root, clean)
}

func (d dirStorage) ReadAll(name string) ([]byte, error) {
	return os.ReadFile(d.full(name))
}

func randHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (d dirStorage) PutAtomic(name string, data []byte) error {
	p := d.full(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp." + randHex()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return d.SyncDir(path.Dir(name))
}

func (d dirStorage) Append(name string, data []byte) error {
	p := d.full(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func (d dirStorage) Sync(name string) error {
	f, err := os.OpenFile(d.full(name), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (d dirStorage) Truncate(name string, size int64) error {
	if err := os.Truncate(d.full(name), size); err != nil {
		return err
	}
	return d.Sync(name)
}

func (d dirStorage) Size(name string) (int64, error) {
	st, err := os.Stat(d.full(name))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (d dirStorage) List(dir string) ([]string, error) {
	ents, err := os.ReadDir(d.full(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

func (d dirStorage) Remove(name string) error {
	err := os.Remove(d.full(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (d dirStorage) RemoveAll(dir string) error { return os.RemoveAll(d.full(dir)) }

func (d dirStorage) MkdirAll(dir string) error {
	return os.MkdirAll(d.full(dir), 0o755)
}

func (d dirStorage) Exists(name string) bool {
	_, err := os.Stat(d.full(name))
	return err == nil
}

func (d dirStorage) CreateExcl(name string, data []byte) error {
	p := d.full(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return d.SyncDir(path.Dir(name))
}

func (d dirStorage) SyncDir(dir string) error {
	f, err := os.Open(d.full(dir))
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// ---------------------------------------------------------------------------
// memStorage: in-memory Storage. Every operation is instantly "durable";
// fault-injecting wrappers model crashes by dropping unsynced appends.
// ---------------------------------------------------------------------------

type memStorage struct {
	mu    sync.Mutex
	files map[string][]byte
}

func newMemStorage() *memStorage { return &memStorage{files: map[string][]byte{}} }

func normName(name string) string {
	n := path.Join("/", name)
	return strings.TrimPrefix(n, "/")
}

func (m *memStorage) ReadAll(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[normName(name)]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (m *memStorage) PutAtomic(name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[normName(name)] = append([]byte(nil), data...)
	return nil
}

func (m *memStorage) Append(name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := normName(name)
	m.files[n] = append(m.files[n], data...)
	return nil
}

func (m *memStorage) Sync(name string) error { return nil }

func (m *memStorage) Truncate(name string, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := normName(name)
	b, ok := m.files[n]
	if !ok {
		return fs.ErrNotExist
	}
	if size > int64(len(b)) {
		return errors.New("encobj: truncate beyond end")
	}
	m.files[n] = append([]byte(nil), b[:size]...)
	return nil
}

func (m *memStorage) Size(name string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[normName(name)]
	if !ok {
		return 0, fs.ErrNotExist
	}
	return int64(len(b)), nil
}

func (m *memStorage) List(dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := normName(dir)
	if prefix != "" {
		prefix += "/"
	}
	set := map[string]bool{}
	for name := range m.files {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if rest == "" {
			continue
		}
		child := rest
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			child = rest[:i]
		}
		set[child] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (m *memStorage) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, normName(name))
	return nil
}

func (m *memStorage) RemoveAll(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := normName(dir)
	if prefix != "" {
		prefix += "/"
	}
	for name := range m.files {
		if name == normName(dir) || strings.HasPrefix(name, prefix) {
			delete(m.files, name)
		}
	}
	return nil
}

func (m *memStorage) MkdirAll(dir string) error { return nil }

func (m *memStorage) Exists(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[normName(name)]
	return ok
}

var errExist = errors.New("encobj: file exists")

func (m *memStorage) CreateExcl(name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := normName(name)
	if _, ok := m.files[n]; ok {
		return errExist
	}
	m.files[n] = append([]byte(nil), data...)
	return nil
}

func (m *memStorage) SyncDir(dir string) error { return nil }
