// Package memfs provides a minimal in-memory file system whose reads can be
// blocked and released on demand, so tests can orchestrate revocation races
// deterministically.
package memfs

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

var ErrNotFound = errors.New("memfs: file not found")

// Dir is an in-memory directory. The zero value is not usable; use NewDir.
type Dir struct {
	mu       sync.Mutex
	files    map[string]string
	gate     chan struct{} // non-nil while reads are blocked
	waiting  atomic.Int32
	released chan struct{}
	relOnce  sync.Once
}

func NewDir() *Dir {
	return &Dir{files: make(map[string]string), released: make(chan struct{})}
}

func (d *Dir) Write(name, content string) {
	d.mu.Lock()
	d.files[name] = content
	d.mu.Unlock()
}

// Block makes every subsequent Read wait until Unblock is called.
func (d *Dir) Block() {
	d.mu.Lock()
	if d.gate == nil {
		d.gate = make(chan struct{})
	}
	d.mu.Unlock()
}

// Unblock releases all current and future blocked reads.
func (d *Dir) Unblock() {
	d.mu.Lock()
	g := d.gate
	d.gate = nil
	d.mu.Unlock()
	if g != nil {
		close(g)
	}
}

// Blocked reports how many readers are currently waiting on the gate.
func (d *Dir) Blocked() int { return int(d.waiting.Load()) }

func (d *Dir) awaitGate() {
	d.mu.Lock()
	g := d.gate
	d.mu.Unlock()
	if g != nil {
		d.waiting.Add(1)
		<-g
		d.waiting.Add(-1)
	}
}

func (d *Dir) Open(name string) (*File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	content, ok := d.files[name]
	if !ok {
		return nil, ErrNotFound
	}
	return &File{dir: d, data: content}, nil
}

// Release is called by the capability registry once the last capability and
// in-flight read referencing this directory are gone. It is idempotent.
func (d *Dir) Release() { d.relOnce.Do(func() { close(d.released) }) }

// Released is closed when Release is first called.
func (d *Dir) Released() <-chan struct{} { return d.released }

// File is an open read stream. Reads already started keep working even after
// the owning capability is revoked; the registry guarantees the Dir object
// outlives the last released File.
type File struct {
	dir  *Dir
	data string
	off  int
}

func (f *File) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	f.dir.awaitGate()
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func (f *File) Close() {}
