package encobj

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
)

// Storage abstracts every filesystem effect the store depends on.
// Implementations must provide these semantics:
//
//   - WriteFileAtomic: data becomes visible under name only after the full
//     byte range of a same-directory temporary file has been written and
//     fsynced, then rename(2) atomically replaces any existing destination,
//     then the containing directory is fsynced. After WriteFileAtomic
//     returns, a crash must reveal either the old contents or the new
//     contents, never a mix, and never a missing destination if one existed.
//   - AppendFile: the bytes are written at the end of the file and fsynced
//     before return. A crash may leave a torn tail (short write) but never
//     garbage in the middle of previously fsynced data.
//   - RemoveFile + SyncDir: removal is durable once SyncDir returns.
//   - Lock: exclusive, process-level, per-directory writer lock (flock).
type Storage interface {
	ReadFile(name string) ([]byte, error)
	WriteFileAtomic(name string, data []byte) error
	AppendFile(name string, data []byte) error
	ReadFileNames() ([]string, error)
	RemoveFile(name string) error
	SyncDir() error
	Lock() (unlock func(), err error)
}

// FSStorage implements Storage on a local filesystem directory.
type FSStorage struct {
	dir string
}

func NewFSStorage(dir string) *FSStorage { return &FSStorage{dir: dir} }

func (fs *FSStorage) path(name string) string {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == ".." {
		panic("encobj: illegal storage name " + name)
	}
	return filepath.Join(fs.dir, name)
}

func (fs *FSStorage) ReadFile(name string) ([]byte, error) {
	b, err := os.ReadFile(fs.path(name))
	if os.IsNotExist(err) {
		return nil, ErrNotExist
	}
	return b, err
}

func (fs *FSStorage) WriteFileAtomic(name string, data []byte) error {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := name + ".tmp-" + hex.EncodeToString(rnd[:])
	p := fs.path(tmp)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(p)
		return err
	}
	if err := os.Rename(p, fs.path(name)); err != nil {
		os.Remove(p)
		return err
	}
	return fs.SyncDir()
}

func (fs *FSStorage) AppendFile(name string, data []byte) error {
	f, err := os.OpenFile(fs.path(name), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
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
	return f.Close()
}

func (fs *FSStorage) ReadFileNames() ([]string, error) {
	ents, err := os.ReadDir(fs.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (fs *FSStorage) RemoveFile(name string) error {
	err := os.Remove(fs.path(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (fs *FSStorage) SyncDir() error {
	d, err := os.Open(fs.dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// processLocks guards against two Store instances in this process writing
// the same directory; flock covers other processes.
var processLocks = struct {
	sync.Mutex
	held map[string]bool
}{held: map[string]bool{}}

// Lock takes an exclusive flock on writer.lock inside the directory, so only
// one process on this machine may write the store at a time.
func (fs *FSStorage) Lock() (func(), error) {
	abs, err := filepath.Abs(fs.dir)
	if err != nil {
		return nil, err
	}
	processLocks.Lock()
	if processLocks.held[abs] {
		processLocks.Unlock()
		return nil, fmt.Errorf("encobj: store already open in this process: %w", ErrLocked)
	}
	processLocks.held[abs] = true
	processLocks.Unlock()
	p := filepath.Join(fs.dir, "writer.lock")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		processLocks.Lock()
		delete(processLocks.held, abs)
		processLocks.Unlock()
		return nil, fmt.Errorf("encobj: another writer holds the store lock: %w", ErrLocked)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		processLocks.Lock()
		delete(processLocks.held, abs)
		processLocks.Unlock()
	}, nil
}

// readAll is a small helper used by tests.
func readAll(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	return buf.Bytes(), err
}
