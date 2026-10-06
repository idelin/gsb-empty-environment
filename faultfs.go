package publisher

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// OpKind identifies a mutating filesystem operation — a potential cut point
// where a failure or crash can be injected.
type OpKind string

const (
	OpCreate  OpKind = "create"
	OpWrite   OpKind = "write"
	OpSync    OpKind = "sync"
	OpRename  OpKind = "rename"
	OpRemove  OpKind = "remove"
	OpSyncDir OpKind = "syncdir"
)

// Op is one entry in the recorded operation log.
type Op struct {
	Kind OpKind
	Path string
}

// ErrCrashed is returned by every operation after a simulated crash until
// Restart is called.
var ErrCrashed = errors.New("faultfs: machine is crashed")

// FaultFS wraps MemFS with failure injection and crash simulation. Every
// mutating operation is a numbered cut point; tests can fail or crash the
// machine at any of them.
type FaultFS struct {
	inner *MemFS

	mu           sync.Mutex
	count        int
	failAt       int   // 1-based op index that returns injectErr; 0 disables
	crashAt      int   // 1-based op index after which the machine crashes; 0 disables
	injectErr    error // error returned at failAt
	crashed      bool
	pendingCrash bool

	// Log records every mutating operation in order.
	Log []Op
}

func NewFaultFS(inner *MemFS) *FaultFS {
	return &FaultFS{inner: inner, injectErr: errors.New("faultfs: injected error")}
}

// FailAt makes the n-th mutating operation fail with err (op not executed).
func (f *FaultFS) FailAt(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAt = n
	if err != nil {
		f.injectErr = err
	}
}

// CrashAt crashes the machine immediately after the n-th mutating operation.
func (f *FaultFS) CrashAt(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.crashAt = n
}

// Count returns how many mutating operations have been observed.
func (f *FaultFS) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

// ResetArming clears fail/crash arming and the operation counter.
func (f *FaultFS) ResetArming() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAt, f.crashAt, f.count = 0, 0, 0
	f.Log = nil
}

// Restart brings the machine back up after a crash.
func (f *FaultFS) Restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.crashed = false
}

func (f *FaultFS) before(kind OpKind, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.crashed {
		return ErrCrashed
	}
	f.count++
	f.Log = append(f.Log, Op{Kind: kind, Path: path})
	if f.failAt == f.count {
		return f.injectErr
	}
	if f.crashAt == f.count {
		f.pendingCrash = true
	}
	return nil
}

func (f *FaultFS) after() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pendingCrash {
		f.pendingCrash = false
		f.crashed = true
		f.inner.Crash()
	}
}

func (f *FaultFS) MkdirAll(dir string) error { return f.inner.MkdirAll(dir) }

func (f *FaultFS) Create(p string) (File, error) {
	if err := f.before(OpCreate, p); err != nil {
		return nil, err
	}
	h, err := f.inner.Create(p)
	f.after()
	if err != nil {
		return nil, err
	}
	return &faultFile{fs: f, inner: h, path: p}, nil
}

func (f *FaultFS) Open(p string) (io.ReadCloser, error) {
	f.mu.Lock()
	if f.crashed {
		f.mu.Unlock()
		return nil, ErrCrashed
	}
	f.mu.Unlock()
	return f.inner.Open(p)
}

func (f *FaultFS) Rename(oldPath, newPath string) error {
	if err := f.before(OpRename, oldPath+" -> "+newPath); err != nil {
		return err
	}
	err := f.inner.Rename(oldPath, newPath)
	f.after()
	return err
}

func (f *FaultFS) Remove(p string) error {
	if err := f.before(OpRemove, p); err != nil {
		return err
	}
	err := f.inner.Remove(p)
	f.after()
	return err
}

func (f *FaultFS) SyncDir(dir string) error {
	if err := f.before(OpSyncDir, dir); err != nil {
		return err
	}
	err := f.inner.SyncDir(dir)
	f.after()
	return err
}

func (f *FaultFS) Exists(p string) bool { return f.inner.Exists(p) }

func (f *FaultFS) ReadDir(dir string) ([]string, error) { return f.inner.ReadDir(dir) }

type faultFile struct {
	fs    *FaultFS
	inner File
	path  string
}

func (h *faultFile) Write(b []byte) (int, error) {
	if err := h.fs.before(OpWrite, h.path); err != nil {
		return 0, err
	}
	n, err := h.inner.Write(b)
	h.fs.after()
	return n, err
}

func (h *faultFile) Sync() error {
	if err := h.fs.before(OpSync, h.path); err != nil {
		return err
	}
	err := h.inner.Sync()
	h.fs.after()
	return err
}

func (h *faultFile) Close() error { return h.inner.Close() }

// String renders an op for test logs, e.g. `sync data/a.txt@2.tmp`.
func (o Op) String() string { return fmt.Sprintf("%s %s", o.Kind, o.Path) }
