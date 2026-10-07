// Package filewrap provides a file wrapper that supports both shared-cursor
// reads and positioned reads on top of an io.ReaderAt backend.
//
// Semantics:
//   - Read consumes the shared cursor; concurrent Reads are serialized on the
//     cursor and the cursor advances only by the number of bytes actually
//     read (short reads included).
//   - ReadAt reads at an explicit offset and never touches the shared cursor;
//     positioned reads may run concurrently with each other.
//   - Close races with reads are adjudicated at the read's entry point: a
//     read that has entered before Close wins the gate is allowed to run to
//     completion; a read that arrives after Close has won fails with
//     ErrClosed. Close itself blocks until in-flight reads finish.
//   - Close is idempotent in effect: the backend is closed exactly once;
//     repeated Close calls return ErrClosed.
package filewrap

import (
	"errors"
	"io"
	"sync"
)

// ErrClosed is returned by operations attempted after the file has been
// closed, and by Close calls after the first.
var ErrClosed = errors.New("filewrap: file already closed")

// ErrNegativeOffset is returned by ReadAt when off is negative.
var ErrNegativeOffset = errors.New("filewrap: negative offset")

// Backend is the storage layer wrapped by File. A virtual in-memory backend
// is enough; the wrapper never assumes more than ReaderAt + Closer.
type Backend interface {
	io.ReaderAt
	io.Closer
}

// File wraps a Backend with a shared read cursor and close gating.
type File struct {
	backend Backend

	// gate adjudicates read entry vs. Close: reads hold RLock for their
	// whole duration, Close takes Lock. Whoever acquires first wins.
	gate   sync.RWMutex
	closed bool

	// curMu serializes shared-cursor reads so that a short read advances
	// the cursor by exactly the bytes it returned, with no interleaving.
	curMu  sync.Mutex
	cursor int64
}

// Open wraps backend in a File. The returned File takes over closing the
// backend.
func Open(backend Backend) *File {
	return &File{backend: backend}
}

// Read reads from the shared cursor and advances it by the number of bytes
// actually read. It returns ErrClosed if the file is already closed when
// the read is entered.
func (f *File) Read(p []byte) (int, error) {
	f.gate.RLock()
	defer f.gate.RUnlock()
	if f.closed {
		return 0, ErrClosed
	}

	// Hold curMu across the backend read so concurrent Reads cannot
	// observe or advance the cursor out from under a short read.
	f.curMu.Lock()
	defer f.curMu.Unlock()

	n, err := f.backend.ReadAt(p, f.cursor)
	if n > 0 {
		f.cursor += int64(n)
	}
	return n, err
}

// ReadAt reads len(p) bytes starting at off without affecting the shared
// cursor. It follows io.ReaderAt semantics: a short read comes with a
// non-nil error (io.EOF at end of input).
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrNegativeOffset
	}
	f.gate.RLock()
	defer f.gate.RUnlock()
	if f.closed {
		return 0, ErrClosed
	}
	return f.backend.ReadAt(p, off)
}

// Offset reports the current shared cursor position.
func (f *File) Offset() int64 {
	f.curMu.Lock()
	defer f.curMu.Unlock()
	return f.cursor
}

// Close closes the file. In-flight reads that entered before Close wins the
// gate run to completion; later operations fail with ErrClosed. The backend
// is closed exactly once; repeated Close calls return ErrClosed.
func (f *File) Close() error {
	f.gate.Lock()
	defer f.gate.Unlock()
	if f.closed {
		return ErrClosed
	}
	f.closed = true
	return f.backend.Close()
}
