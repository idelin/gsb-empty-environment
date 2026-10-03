package roaring

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// ErrDiskFull is returned by storage operations when the (simulated or real)
// disk has no space left.
var ErrDiskFull = errors.New("roaring: no space left on device")

var errNotExist = errors.New("roaring: file does not exist")

// VFile is a writable file handle. Data written to it is only guaranteed to
// survive a crash after Sync returns successfully.
type VFile interface {
	Write(p []byte) (int, error)
	Sync() error
	Close() error
}

// VFS abstracts the filesystem so that fault injection (power loss, disk
// full, torn writes) can be simulated deterministically in tests.
type VFS interface {
	// OpenFile opens name for appending; with truncate=true it first resets
	// the file to zero length (creating it if necessary).
	OpenFile(name string, truncate bool) (VFile, error)
	ReadFile(name string) ([]byte, error)
	Rename(old, new string) error
	Remove(name string) error
	Truncate(name string, size int64) error
}

// SimFS is an in-memory VFS that models the page cache: writes go to a dirty
// buffer and only become durable (committed) on Sync. Crash() drops all dirty
// data, simulating sudden power loss.
//
// Simplification: metadata operations (Rename/Remove) are treated as
// immediately durable, as if the directory were fsynced.
type SimFS struct {
	mu        sync.Mutex
	committed map[string][]byte
	dirty     map[string][]byte
	// DiskFull, when true, makes every Write and Sync fail with ErrDiskFull.
	DiskFull bool
}

// NewSimFS returns an empty simulated filesystem.
func NewSimFS() *SimFS {
	return &SimFS{
		committed: make(map[string][]byte),
		dirty:     make(map[string][]byte),
	}
}

func (fs *SimFS) OpenFile(name string, truncate bool) (VFile, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if truncate {
		delete(fs.committed, name)
		fs.dirty[name] = nil
	} else if _, ok := fs.dirty[name]; !ok {
		fs.dirty[name] = append([]byte(nil), fs.committed[name]...)
	}
	return &simFile{fs: fs, name: name}, nil
}

func (fs *SimFS) ReadFile(name string) ([]byte, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if d, ok := fs.dirty[name]; ok {
		return append([]byte(nil), d...), nil
	}
	if c, ok := fs.committed[name]; ok {
		return append([]byte(nil), c...), nil
	}
	return nil, errNotExist
}

func (fs *SimFS) Rename(old, new string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	d, dOK := fs.dirty[old]
	c, cOK := fs.committed[old]
	if !dOK && !cOK {
		return errNotExist
	}
	if dOK {
		fs.dirty[new] = d
		delete(fs.dirty, old)
	}
	if cOK {
		fs.committed[new] = c
		delete(fs.committed, old)
	}
	return nil
}

func (fs *SimFS) Remove(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.dirty, name)
	delete(fs.committed, name)
	return nil
}

func (fs *SimFS) Truncate(name string, size int64) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	content, ok := fs.dirty[name]
	if !ok {
		content = fs.committed[name]
	}
	if int64(len(content)) < size {
		return errors.New("roaring: truncate cannot extend a file")
	}
	fs.dirty[name] = append([]byte(nil), content[:size]...)
	return nil
}

// Crash simulates sudden power loss: all data that was written but not
// synced is lost.
func (fs *SimFS) Crash() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.dirty = make(map[string][]byte)
}

// SimulateTornWrite appends partially flushed bytes directly to the
// committed state, mimicking a torn sector write during power loss.
func (fs *SimFS) SimulateTornWrite(name string, p []byte) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.committed[name] = append(fs.committed[name], p...)
}

// CorruptCommitted flips one byte of a committed file, mimicking silent disk
// corruption.
func (fs *SimFS) CorruptCommitted(name string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if c, ok := fs.committed[name]; ok && len(c) > 0 {
		c[len(c)-1] ^= 0xff
	}
	if d, ok := fs.dirty[name]; ok && len(d) > 0 {
		d[len(d)-1] ^= 0xff
	}
}

type simFile struct {
	fs   *SimFS
	name string
}

func (f *simFile) Write(p []byte) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if f.fs.DiskFull {
		return 0, ErrDiskFull
	}
	f.fs.dirty[f.name] = append(f.fs.dirty[f.name], p...)
	return len(p), nil
}

func (f *simFile) Sync() error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if f.fs.DiskFull {
		return ErrDiskFull
	}
	f.fs.committed[f.name] = append([]byte(nil), f.fs.dirty[f.name]...)
	return nil
}

func (f *simFile) Close() error { return nil }

// OSFS is a VFS backed by a real directory on disk.
type OSFS struct{ Dir string }

func (fs OSFS) path(name string) string { return filepath.Join(fs.Dir, name) }

func (fs OSFS) OpenFile(name string, truncate bool) (VFile, error) {
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if truncate {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	return os.OpenFile(fs.path(name), flag, 0o644)
}

func (fs OSFS) ReadFile(name string) ([]byte, error) {
	data, err := os.ReadFile(fs.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotExist
	}
	return data, err
}

func (fs OSFS) Rename(old, new string) error { return os.Rename(fs.path(old), fs.path(new)) }

func (fs OSFS) Remove(name string) error {
	if err := os.Remove(fs.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (fs OSFS) Truncate(name string, size int64) error {
	return os.Truncate(fs.path(name), size)
}
