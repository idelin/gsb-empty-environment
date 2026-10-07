// Package sortexec implements a query executor that spills sorted runs to
// disk, merges them, and delivers ordered batches to its caller.
//
// The executor is cancellation-safe at every stage (produce, merge,
// deliver): batches already handed to the caller stay valid, while
// undelivered spill files and iterators are closed and removed exactly
// once. The file system is injectable for deterministic fault injection,
// and phase barriers let tests pause or cancel at exact points.
package sortexec

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// WriteFile is a writable spill file.
type WriteFile interface {
	io.Writer
	io.Closer
}

// ReadFile is a readable spill file.
type ReadFile interface {
	io.Reader
	io.Closer
}

// FileSystem abstracts spill-file storage so tests can inject failures.
type FileSystem interface {
	Create(name string) (WriteFile, error)
	Open(name string) (ReadFile, error)
	Remove(name string) error
}

// OSFS stores spill files under a directory on the local disk.
type OSFS struct {
	Dir string
}

func (fs OSFS) Create(name string) (WriteFile, error) {
	return os.Create(filepath.Join(fs.Dir, name))
}

func (fs OSFS) Open(name string) (ReadFile, error) {
	return os.Open(filepath.Join(fs.Dir, name))
}

func (fs OSFS) Remove(name string) error {
	return os.Remove(filepath.Join(fs.Dir, name))
}

// MemFS is an in-memory FileSystem. Writes are committed on Close.
type MemFS struct {
	mu    sync.Mutex
	files map[string][]byte
}

func NewMemFS() *MemFS {
	return &MemFS{files: make(map[string][]byte)}
}

func (fs *MemFS) Create(name string) (WriteFile, error) {
	return &memWriteFile{fs: fs, name: name}, nil
}

func (fs *MemFS) Open(name string) (ReadFile, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, ok := fs.files[name]
	if !ok {
		return nil, errors.New("sortexec: open " + name + ": no such file")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (fs *MemFS) Remove(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.files, name)
	return nil
}

// Files returns the names of committed files, sorted.
func (fs *MemFS) Files() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	names := make([]string, 0, len(fs.files))
	for name := range fs.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type memWriteFile struct {
	fs     *MemFS
	name   string
	buf    bytes.Buffer
	closed bool
}

func (w *memWriteFile) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

func (w *memWriteFile) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()
	w.fs.files[w.name] = append([]byte(nil), w.buf.Bytes()...)
	return nil
}

// ErrInjected is the default error returned by FaultFS.
var ErrInjected = errors.New("sortexec: injected fault")

// FaultFS wraps a FileSystem and fails the Nth call of a chosen kind.
// A zero *At value disables injection for that operation.
type FaultFS struct {
	Inner FileSystem
	Err   error // defaults to ErrInjected

	CreateAt int // fail the Nth Create call
	OpenAt   int // fail the Nth Open call
	WriteAt  int // fail the Nth Write call (across all files)
	ReadAt   int // fail the Nth Read call (across all files)

	mu                            sync.Mutex
	creates, opens, writes, reads int
}

func (f *FaultFS) injected() error {
	if f.Err != nil {
		return f.Err
	}
	return ErrInjected
}

func (f *FaultFS) Create(name string) (WriteFile, error) {
	f.mu.Lock()
	f.creates++
	n := f.creates
	f.mu.Unlock()
	if n == f.CreateAt {
		return nil, f.injected()
	}
	w, err := f.Inner.Create(name)
	if err != nil {
		return nil, err
	}
	return &faultWriteFile{wf: w, fs: f}, nil
}

func (f *FaultFS) Open(name string) (ReadFile, error) {
	f.mu.Lock()
	f.opens++
	n := f.opens
	f.mu.Unlock()
	if n == f.OpenAt {
		return nil, f.injected()
	}
	r, err := f.Inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &faultReadFile{rf: r, fs: f}, nil
}

func (f *FaultFS) Remove(name string) error {
	return f.Inner.Remove(name)
}

type faultWriteFile struct {
	wf WriteFile
	fs *FaultFS
}

func (w *faultWriteFile) Write(p []byte) (int, error) {
	w.fs.mu.Lock()
	w.fs.writes++
	n := w.fs.writes
	w.fs.mu.Unlock()
	if n == w.fs.WriteAt {
		return 0, w.fs.injected()
	}
	return w.wf.Write(p)
}

func (w *faultWriteFile) Close() error { return w.wf.Close() }

type faultReadFile struct {
	rf ReadFile
	fs *FaultFS
}

func (r *faultReadFile) Read(p []byte) (int, error) {
	r.fs.mu.Lock()
	r.fs.reads++
	n := r.fs.reads
	r.fs.mu.Unlock()
	if n == r.fs.ReadAt {
		return 0, r.fs.injected()
	}
	return r.rf.Read(p)
}

func (r *faultReadFile) Close() error { return r.rf.Close() }
