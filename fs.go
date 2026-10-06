package sortexec

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// File is a spill file handle. Implementations must return an error from
// Close on any use after the first Close.
type File interface {
	io.Reader
	io.Writer
	io.Closer
}

// FileSystem abstracts the storage used for spill runs so tests can inject
// faults and count resource usage.
type FileSystem interface {
	Create(name string) (File, error)
	Open(name string) (File, error)
	Remove(name string) error
}

// OSFS stores spill files as regular files under Dir.
type OSFS struct {
	Dir string
}

func (fs OSFS) path(name string) string { return filepath.Join(fs.Dir, name) }

func (fs OSFS) Create(name string) (File, error) { return os.Create(fs.path(name)) }

func (fs OSFS) Open(name string) (File, error) { return os.Open(fs.path(name)) }

func (fs OSFS) Remove(name string) error {
	err := os.Remove(fs.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
