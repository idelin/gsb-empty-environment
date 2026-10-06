// Package batch implements a restartable batch processing library.
//
// A batch freezes its input manifest (file list + content digests) when it
// starts. Restarts verify the versions of unprocessed items against the
// frozen manifest; files created after the freeze are never auto-included.
// Processed results are bound to the digest of the input they were computed
// from, and results are never reused across batches.
package batch

import (
	"crypto/sha256"
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FS is a minimal virtual directory abstraction. It lets the runner and the
// tests operate on real directories or on in-memory trees interchangeably.
type FS interface {
	// ReadFile returns the content of path, or os.ErrNotExist wrapped error.
	ReadFile(path string) ([]byte, error)
	// WriteFile atomically replaces the content of path.
	WriteFile(path string, data []byte) error
	// Remove deletes path. Removing a missing path is not an error.
	Remove(path string) error
	// List returns the relative slash-separated paths of all regular files
	// under dir, sorted. A missing dir yields an empty list.
	List(dir string) ([]string, error)
}

// DigestOf returns the content digest used throughout the library.
func DigestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// OSFS maps the FS interface onto the local filesystem rooted at Root.
type OSFS struct {
	Root string
}

func (o OSFS) full(path string) string {
	return filepath.Join(o.Root, filepath.FromSlash(path))
}

func (o OSFS) ReadFile(path string) ([]byte, error) {
	return ioutil.ReadFile(o.full(path))
}

func (o OSFS) WriteFile(path string, data []byte) error {
	full := o.full(path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	tmp := full + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

func (o OSFS) Remove(path string) error {
	err := os.Remove(o.full(path))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (o OSFS) List(dir string) ([]string, error) {
	var out []string
	base := o.full(dir)
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		if strings.HasSuffix(rel, ".tmp") {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, nil
}

// MemFS is an in-memory FS used by tests to simulate directory changes
// (delete, replace, add) between runs and across restarts.
type MemFS struct {
	mu    sync.Mutex
	files map[string][]byte
}

func NewMemFS() *MemFS {
	return &MemFS{files: make(map[string][]byte)}
}

func (m *MemFS) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (m *MemFS) WriteFile(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[path] = cp
	return nil
}

func (m *MemFS) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, path)
	return nil
}

func (m *MemFS) List(dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := ""
	if dir != "" && dir != "." {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	var out []string
	for p := range m.files {
		if strings.HasPrefix(p, prefix) {
			out = append(out, strings.TrimPrefix(p, prefix))
		}
	}
	sort.Strings(out)
	return out, nil
}
