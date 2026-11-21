package appendhandle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// MemFile is an in-process [File] implementation used by tests. It records
// hole punches and refuses overlapping writes, which would prove that a
// reservation range was silently recycled.
type MemFile struct {
	mu      sync.Mutex
	data    []byte
	written []interval
	holes   []interval

	// HoleCount grows every time a non-empty range is punched.
	HoleCount int
}

type interval struct {
	lo int64
	hi int64
}

// NewMemFile returns an empty memory-backed file.
func NewMemFile() *MemFile { return &MemFile{} }

// Data returns a copy of the full raw storage, including uncommitted bytes.
func (m *MemFile) Data() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]byte, len(m.data))
	copy(out, m.data)
	return out
}

// Holes returns a sorted copy of every punched interval.
func (m *MemFile) Holes() []interval {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]interval, len(m.holes))
	copy(out, m.holes)
	return out
}

// RawSize is the current truncate-based storage length.
func (m *MemFile) RawSize() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.data))
}

func (m *MemFile) Truncate(size int64) error {
	if size < 0 {
		return ErrNegativeOffset
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if size < int64(len(m.data)) {
		m.data = m.data[:size]
	} else if size > int64(len(m.data)) {
		grown := make([]byte, size-int64(len(m.data)))
		m.data = append(m.data, grown...)
	}
	return nil
}

func (m *MemFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrNegativeOffset
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// WriteAt fails if any target byte was previously written. Every volume
// reservation owns a disjoint range, so an overlap means the allocator gave a
// range to a second request.
func (m *MemFile) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrNegativeOffset
	}
	end := off + int64(len(p))
	m.mu.Lock()
	defer m.mu.Unlock()
	if end > int64(len(m.data)) {
		return 0, fmt.Errorf("appendhandle: write [%d,%d) beyond storage size %d", off, end, len(m.data))
	}
	for _, iv := range m.written {
		if off < iv.hi && end > iv.lo {
			return 0, fmt.Errorf("appendhandle: illegal overwrite of committed bytes [%d,%d) by [%d,%d)", iv.lo, iv.hi, off, end)
		}
	}
	copy(m.data[off:end], p)
	m.written = append(m.written, interval{lo: off, hi: end})
	sort.Slice(m.written, func(i, j int) bool { return m.written[i].lo < m.written[j].lo })
	return len(p), nil
}

func (m *MemFile) Sync() error { return nil }

// PunchHole zeroes the range, merges it into the recorded hole list and
// removes the bytes from the written set, matching the read-back semantics of
// a deallocated filesystem hole.
func (m *MemFile) PunchHole(off, length int64) error {
	if off < 0 || length < 0 {
		return ErrNegativeOffset
	}
	if length == 0 {
		return nil
	}
	end := off + length
	m.mu.Lock()
	defer m.mu.Unlock()
	if end > int64(len(m.data)) {
		return fmt.Errorf("appendhandle: punch [%d,%d) beyond storage size %d", off, end, len(m.data))
	}
	for i := off; i < end; i++ {
		m.data[i] = 0
	}
	m.holes = append(m.holes, interval{lo: off, hi: end})
	m.HoleCount++

	kept := m.written[:0]
	for _, iv := range m.written {
		if iv.hi <= off || iv.lo >= end {
			kept = append(kept, iv)
		}
	}
	m.written = kept
	return nil
}

// FaultFile wraps a [MemFile] and injects storage faults to exercise short
// writes, transient errors and failing syncs under concurrency.
type FaultFile struct {
	inner *MemFile

	mu sync.Mutex
	// MaxWrite forces each WriteAt to accept at most MaxWrite bytes. Zero
	// means unlimited.
	MaxWrite int
	// FailEvery causes every FailEvery-th WriteAt to return ErrInjected (a
	// value of 1 fails every write). Zero disables it. The counter only
	// advances on attempts that are not zero-length.
	FailEvery int
	// FailsLeft bounds how many injected write errors remain. Negative means
	// unlimited once FailEvery is configured.
	FailsLeft int
	// SyncFails is the number of Sync calls that still return ErrInjected.
	SyncFails int

	writeCalls int
	syncCalls  int
}

// ErrInjected is the sentinel fault returned by [FaultFile].
var ErrInjected = errors.New("appendhandle: injected storage fault")

// NewFaultFile wraps an existing [MemFile] (or allocates one when nil).
func NewFaultFile(inner *MemFile) *FaultFile {
	if inner == nil {
		inner = NewMemFile()
	}
	return &FaultFile{inner: inner, FailsLeft: -1}
}

// MemFile exposes the wrapped memory file for assertions.
func (f *FaultFile) MemFile() *MemFile { return f.inner }

// WriteCalls is the number of WriteAt invocations seen so far.
func (f *FaultFile) WriteCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeCalls
}

// SyncCalls is the number of Sync invocations seen so far.
func (f *FaultFile) SyncCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncCalls
}

func (f *FaultFile) ReadAt(p []byte, off int64) (int, error) {
	return f.inner.ReadAt(p, off)
}

func (f *FaultFile) WriteAt(p []byte, off int64) (int, error) {
	limit := len(p)

	f.mu.Lock()
	f.writeCalls++
	shouldFail := false
	if f.FailEvery > 0 && len(p) > 0 && f.writeCalls%f.FailEvery == 0 && f.FailsLeft != 0 {
		shouldFail = true
		if f.FailsLeft > 0 {
			f.FailsLeft--
		}
	}
	if f.MaxWrite > 0 && f.MaxWrite < limit {
		limit = f.MaxWrite
	}
	f.mu.Unlock()

	if shouldFail {
		return 0, ErrInjected
	}
	if limit < len(p) {
		// Short write without an error: the caller must retry the tail.
		n, err := f.inner.WriteAt(p[:limit], off)
		return n, err
	}
	return f.inner.WriteAt(p, off)
}

func (f *FaultFile) Truncate(size int64) error { return f.inner.Truncate(size) }

func (f *FaultFile) Sync() error {
	f.mu.Lock()
	f.syncCalls++
	fail := f.SyncFails > 0
	if fail {
		f.SyncFails--
	}
	f.mu.Unlock()
	if fail {
		return ErrInjected
	}
	return f.inner.Sync()
}

func (f *FaultFile) PunchHole(off, length int64) error {
	return f.inner.PunchHole(off, length)
}

// String dumps the raw storage for failure messages.
func (f *FaultFile) String() string {
	return fmt.Sprintf("FaultFile(%s)", bytes.TrimRight(f.inner.Data(), "\x00"))
}
