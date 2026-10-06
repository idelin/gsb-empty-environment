package publisher

import (
	"io"
	"os"
	"path/filepath"
)

// File is a writable file handle. Sync makes written content durable.
type File interface {
	io.Writer
	Sync() error
	Close() error
}

// FS is the minimal storage surface the publisher needs. All durability
// guarantees are expressed through File.Sync and SyncDir, so a fault
// injection implementation can model exactly which operations survive a
// crash.
type FS interface {
	MkdirAll(dir string) error
	Create(name string) (File, error)
	Append(name string) (File, error)
	ReadFile(name string) ([]byte, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
	SyncDir(dir string) error
}

// OSFS implements FS against the local operating system.
type OSFS struct{}

func (OSFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0755) }

func (OSFS) Create(name string) (File, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
}

func (OSFS) Append(name string) (File, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
}

func (OSFS) ReadFile(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, rerr := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr == io.EOF {
			return buf, nil
		}
		if rerr != nil {
			return nil, rerr
		}
	}
}

func (OSFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

func (OSFS) Remove(name string) error { return os.Remove(name) }

func (OSFS) SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Join joins path elements; kept as a helper so in-memory FS
// implementations share the same separators as the OS.
func Join(elem ...string) string { return filepath.Join(elem...) }
