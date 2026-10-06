package sortexec

import (
	"errors"
	"sync"
	"sync/atomic"
)

// FaultFS wraps another FileSystem with call counting and fault injection.
// FailXAt fields are 1-based call indexes: the Nth call of that kind fails
// with Err. A zero value disables injection for that operation.
type FaultFS struct {
	Inner FileSystem
	Err   error

	FailCreateAt int64
	FailOpenAt   int64
	FailWriteAt  int64
	FailReadAt   int64

	creates atomic.Int64
	opens   atomic.Int64
	writes  atomic.Int64
	reads   atomic.Int64

	mu     sync.Mutex
	open   map[*faultFile]string
	closes map[*faultFile]int
}

// NewFaultFS wraps inner with fault injection and resource accounting.
func NewFaultFS(inner FileSystem) *FaultFS {
	return &FaultFS{
		Inner:  inner,
		open:   make(map[*faultFile]string),
		closes: make(map[*faultFile]int),
	}
}

func (f *FaultFS) injected() error {
	if f.Err != nil {
		return f.Err
	}
	return errors.New("sortexec: injected fault")
}

func (f *FaultFS) track(ff *faultFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.open[ff] = ff.name
}

// Create implements FileSystem.
func (f *FaultFS) Create(name string) (File, error) {
	if n := f.creates.Add(1); f.FailCreateAt != 0 && n == f.FailCreateAt {
		return nil, f.injected()
	}
	file, err := f.Inner.Create(name)
	if err != nil {
		return nil, err
	}
	ff := &faultFile{File: file, fs: f, name: name}
	f.track(ff)
	return ff, nil
}

// Open implements FileSystem.
func (f *FaultFS) Open(name string) (File, error) {
	if n := f.opens.Add(1); f.FailOpenAt != 0 && n == f.FailOpenAt {
		return nil, f.injected()
	}
	file, err := f.Inner.Open(name)
	if err != nil {
		return nil, err
	}
	ff := &faultFile{File: file, fs: f, name: name}
	f.track(ff)
	return ff, nil
}

// Remove implements FileSystem.
func (f *FaultFS) Remove(name string) error { return f.Inner.Remove(name) }

// CreateCalls, OpenCalls, WriteCalls and ReadCalls report how many calls of
// each kind have been made so far, so tests can arm faults relative to the
// current position, e.g. ffs.FailReadAt = ffs.ReadCalls() + 1.
func (f *FaultFS) CreateCalls() int64 { return f.creates.Load() }
func (f *FaultFS) OpenCalls() int64   { return f.opens.Load() }
func (f *FaultFS) WriteCalls() int64  { return f.writes.Load() }
func (f *FaultFS) ReadCalls() int64   { return f.reads.Load() }

// OpenHandles reports how many handles are currently open.
func (f *FaultFS) OpenHandles() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.open)
}

// OverClosed returns the names of handles closed more than once.
func (f *FaultFS) OverClosed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for ff, n := range f.closes {
		if n > 1 {
			names = append(names, ff.name)
		}
	}
	return names
}

type faultFile struct {
	File
	fs   *FaultFS
	name string
}

func (ff *faultFile) Write(p []byte) (int, error) {
	if n := ff.fs.writes.Add(1); ff.fs.FailWriteAt != 0 && n == ff.fs.FailWriteAt {
		return 0, ff.fs.injected()
	}
	return ff.File.Write(p)
}

func (ff *faultFile) Read(p []byte) (int, error) {
	if n := ff.fs.reads.Add(1); ff.fs.FailReadAt != 0 && n == ff.fs.FailReadAt {
		return 0, ff.fs.injected()
	}
	return ff.File.Read(p)
}

func (ff *faultFile) Close() error {
	ff.fs.mu.Lock()
	ff.fs.closes[ff]++
	delete(ff.fs.open, ff)
	ff.fs.mu.Unlock()
	return ff.File.Close()
}
