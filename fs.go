package keydir

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is the minimal append/read handle the store needs.
type File interface {
	// Append writes p at the end of the file.
	Append(p []byte) (int, error)
	// ReadAt reads len(p) bytes at offset off.
	ReadAt(p []byte, off int64) (int, error)
	// Size returns the current file size.
	Size() (int64, error)
	// Truncate shrinks the file to size bytes.
	Truncate(size int64) error
	// Sync makes previously appended bytes durable.
	Sync() error
	Close() error
}

// FS abstracts file creation, lookup and atomic replace so tests can
// inject crashes, partial flushes and corruption.
type FS interface {
	// Create creates (or truncates) a file for appending.
	Create(name string) (File, error)
	// Open opens an existing file for reading and appending.
	Open(name string) (File, error)
	// List returns the names of all regular files.
	List() ([]string, error)
	// Remove deletes a file; missing files are not an error.
	Remove(name string) error
	// Rename atomically replaces dst with src.
	Rename(src, dst string) error
}

// osFS is the production FS backed by a real directory.
type osFS struct {
	dir string
}

// NewOSFS returns an FS rooted at dir, creating it if needed.
func NewOSFS(dir string) (FS, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &osFS{dir: dir}, nil
}

type osFile struct {
	f *os.File
}

func (f *osFile) Append(p []byte) (int, error) {
	st, err := f.f.Stat()
	if err != nil {
		return 0, err
	}
	return f.f.WriteAt(p, st.Size())
}
func (f *osFile) ReadAt(p []byte, off int64) (int, error) {
	return f.f.ReadAt(p, off)
}
func (f *osFile) Size() (int64, error) {
	st, err := f.f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
func (f *osFile) Sync() error  { return f.f.Sync() }
func (f *osFile) Close() error { return f.f.Close() }
func (f *osFile) Truncate(size int64) error {
	return f.f.Truncate(size)
}

func (fs *osFS) Create(name string) (File, error) {
	f, err := os.OpenFile(filepath.Join(fs.dir, name), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return &osFile{f: f}, nil
}

func (fs *osFS) Open(name string) (File, error) {
	f, err := os.OpenFile(filepath.Join(fs.dir, name), os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return &osFile{f: f}, nil
}

func (fs *osFS) List() ([]string, error) {
	infos, err := ioutil.ReadDir(fs.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, info := range infos {
		if info.Mode().IsRegular() && !strings.HasPrefix(info.Name(), ".") {
			names = append(names, info.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (fs *osFS) Remove(name string) error {
	err := os.Remove(filepath.Join(fs.dir, name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (fs *osFS) Rename(src, dst string) error {
	return os.Rename(filepath.Join(fs.dir, src), filepath.Join(fs.dir, dst))
}
