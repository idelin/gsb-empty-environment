package atomicpub

import (
	"errors"
	"os"
	"sync"
)

// ErrCrash is returned by FaultFS to simulate a power-loss / process crash.
// The operation may or may not have reached disk; recovery must tolerate both.
var ErrCrash = errors.New("atomicpub: simulated crash")

// FS abstracts every filesystem mutation the store performs so tests can
// inject write failures, rename failures and crashes at exact steps.
type FS interface {
	WriteFile(path string, data []byte) error // create-excl, fsync before close
	ReadFile(path string) ([]byte, error)
	Rename(oldPath, newPath string) error
	MkdirAll(path string) error
	Remove(path string) error
	RemoveAll(path string) error
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (os.FileInfo, error)
	SyncDir(path string) error
}

// osFS is the production implementation. Every file write is fsynced and
// every directory that gains/loses an entry is fsynced by the caller.
type osFS struct{}

func (osFS) WriteFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
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

func (osFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (osFS) Rename(o, n string) error             { return os.Rename(o, n) }
func (osFS) MkdirAll(p string) error              { return os.MkdirAll(p, 0o755) }
func (osFS) Remove(p string) error                { return os.Remove(p) }
func (osFS) RemoveAll(p string) error             { return os.RemoveAll(p) }
func (osFS) ReadDir(p string) ([]os.DirEntry, error) {
	return os.ReadDir(p)
}
func (osFS) Stat(p string) (os.FileInfo, error) { return os.Stat(p) }

func (osFS) SyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// FaultFS wraps an FS and invokes a hook before every operation. Returning a
// non-nil error from the hook fails the operation without touching disk;
// returning ErrCrash simulates a restart at that exact point.
type FaultFS struct {
	inner FS

	mu   sync.Mutex
	hook func(op, path string) error
}

func NewFaultFS(inner FS) *FaultFS { return &FaultFS{inner: inner} }

// SetHook installs the fault hook. op is one of "write", "read", "rename",
// "mkdir", "remove", "removeall", "readdir", "stat", "syncdir".
func (f *FaultFS) SetHook(h func(op, path string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hook = h
}

func (f *FaultFS) check(op, path string) error {
	f.mu.Lock()
	h := f.hook
	f.mu.Unlock()
	if h != nil {
		return h(op, path)
	}
	return nil
}

func (f *FaultFS) WriteFile(p string, d []byte) error {
	if err := f.check("write", p); err != nil {
		return err
	}
	return f.inner.WriteFile(p, d)
}
func (f *FaultFS) ReadFile(p string) ([]byte, error) {
	if err := f.check("read", p); err != nil {
		return nil, err
	}
	return f.inner.ReadFile(p)
}
func (f *FaultFS) Rename(o, n string) error {
	if err := f.check("rename", o+" -> "+n); err != nil {
		return err
	}
	return f.inner.Rename(o, n)
}
func (f *FaultFS) MkdirAll(p string) error {
	if err := f.check("mkdir", p); err != nil {
		return err
	}
	return f.inner.MkdirAll(p)
}
func (f *FaultFS) Remove(p string) error {
	if err := f.check("remove", p); err != nil {
		return err
	}
	return f.inner.Remove(p)
}
func (f *FaultFS) RemoveAll(p string) error {
	if err := f.check("removeall", p); err != nil {
		return err
	}
	return f.inner.RemoveAll(p)
}
func (f *FaultFS) ReadDir(p string) ([]os.DirEntry, error) {
	if err := f.check("readdir", p); err != nil {
		return nil, err
	}
	return f.inner.ReadDir(p)
}
func (f *FaultFS) Stat(p string) (os.FileInfo, error) {
	if err := f.check("stat", p); err != nil {
		return nil, err
	}
	return f.inner.Stat(p)
}
func (f *FaultFS) SyncDir(p string) error {
	if err := f.check("syncdir", p); err != nil {
		return err
	}
	return f.inner.SyncDir(p)
}
