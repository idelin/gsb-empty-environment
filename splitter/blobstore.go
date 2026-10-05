package splitter

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// BlobStore is the injectable target abstraction. Both the online and the
// archive target are backed by one of these.
type BlobStore interface {
	Put(name string, data []byte) error // idempotent: same name+data is a no-op
	Get(name string) ([]byte, error)    // ErrNotFound when absent
	Delete(name string) error           // idempotent: absent is a no-op
	List() ([]string, error)
}

// FileAdapter simulates a remote target with a local directory.
// Writes go through tmp+rename+fsync so a committed Put survives a crash.
type FileAdapter struct {
	Dir string
}

// NewFileAdapter creates (if needed) and returns a directory-backed target.
func NewFileAdapter(dir string) (*FileAdapter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileAdapter{Dir: dir}, nil
}

// Put writes data atomically and fsyncs the containing directory.
func (f *FileAdapter) Put(name string, data []byte) error {
	path := filepath.Join(f.Dir, name)
	tmp := path + ".tmp"
	w, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return fsyncDir(f.Dir)
}

// Get reads a blob; missing blobs yield ErrNotFound.
func (f *FileAdapter) Get(name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(f.Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

// Delete removes a blob; missing blobs are a no-op.
func (f *FileAdapter) Delete(name string) error {
	err := os.Remove(filepath.Join(f.Dir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return fsyncDir(f.Dir)
}

// List returns all committed blob names (stray tmp files excluded).
func (f *FileAdapter) List() ([]string, error) {
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) == ".tmp" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
