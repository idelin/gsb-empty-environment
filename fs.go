// Package segvm implements a crash-recoverable, versioned segment manifest
// manager using only the Go standard library.
//
// Storage layout under the managed directory:
//
//	manifest.log          append-only log of framed version records
//	segments/<sha256>.seg immutable content-addressed segment files
//
// A publish is atomic: either the whole new version is visible after
// reopening, or the previous version still is. The commit point is the
// fsync of the appended manifest record.
package segvm

import (
	"io"
	"os"
	"sort"
)

// File is the minimal file handle the manager needs.
type File interface {
	io.Reader
	io.Writer
	io.Closer
	Sync() error
	Truncate(size int64) error
}

// FS abstracts every storage operation the manager relies on. Each method
// maps to exactly one durable-operation class (write, sync, rename,
// directory sync, ...) so tests can inject a failure or a simulated process
// exit at a precise publish step.
type FS interface {
	MkdirAll(dir string) error
	Create(path string) (File, error)
	OpenAppend(path string) (File, error)
	Open(path string) (File, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
	ReadDir(dir string) ([]string, error)
	SyncDir(dir string) error
}

// OSFS is the real filesystem implementation of FS.
type OSFS struct{}

func (OSFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

func (OSFS) Create(path string) (File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

func (OSFS) OpenAppend(path string) (File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func (OSFS) Open(path string) (File, error) { return os.Open(path) }

func (OSFS) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

func (OSFS) Remove(path string) error { return os.Remove(path) }

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

func (OSFS) SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// readAll reads an entire file through the FS abstraction. A missing file
// yields nil, nil (treated as empty).
func readAll(fs FS, path string) ([]byte, error) {
	f, err := fs.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
