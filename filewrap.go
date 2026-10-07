// Package filewrap wraps a positioned-read backend with a shared
// sequential cursor. A File is safe for concurrent use: shared-cursor
// reads are serialized against each other, positioned reads never touch
// the cursor, and Close is arbitrated against reads by entry point —
// a read that has already entered may run to completion.
package filewrap

import (
	"errors"
	"io"
	"sync"
)

// ErrClosed is returned by operations that arrive after Close.
var ErrClosed = errors.New("filewrap: use of closed file")

// Backend is the storage layer wrapped by File.
type Backend interface {
	io.ReaderAt
	io.Closer
}

// File provides shared-cursor reads and positioned reads over a Backend.
type File struct {
	backend Backend

	// gate arbitrates reads against Close: a read holding the read
	// gate finishes even while a Close is pending; reads that arrive
	// after Close observe closed and fail with ErrClosed.
	gate   sync.RWMutex
	closed bool

	// curMu serializes shared-cursor reads so concurrent Reads each
	// observe a distinct, monotonically advancing offset.
	curMu sync.Mutex
	off   int64
}

// New wraps b in a File whose shared cursor starts at offset 0.
func New(b Backend) *File {
	return &File{backend: b}
}

// Read consumes the shared cursor. It advances the cursor only by the
// number of bytes actually returned, so short reads leave the cursor
// immediately after the last byte delivered.
func (f *File) Read(p []byte) (int, error) {
	f.gate.RLock()
	defer f.gate.RUnlock()
	if f.closed {
		return 0, ErrClosed
	}
	f.curMu.Lock()
	defer f.curMu.Unlock()
	n, err := f.backend.ReadAt(p, f.off)
	f.off += int64(n)
	return n, err
}

// ReadAt reads at an absolute offset. It never observes or mutates the
// shared cursor, and runs concurrently with other positioned reads.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	f.gate.RLock()
	defer f.gate.RUnlock()
	if f.closed {
		return 0, ErrClosed
	}
	return f.backend.ReadAt(p, off)
}

// Offset reports the current shared cursor position.
func (f *File) Offset() int64 {
	f.gate.RLock()
	defer f.gate.RUnlock()
	f.curMu.Lock()
	defer f.curMu.Unlock()
	return f.off
}

// Close closes the backend. It waits for reads that have already
// entered to complete, then rejects all further operations with
// ErrClosed. Only the first Close closes the backend; later calls
// return ErrClosed.
func (f *File) Close() error {
	f.gate.Lock()
	defer f.gate.Unlock()
	if f.closed {
		return ErrClosed
	}
	f.closed = true
	return f.backend.Close()
}
