// Package appendhandle implements append-only write handles for an audit
// volume.
//
// The underlying storage is only allowed to grow through reservations handed
// out by [Volume.Reserve]. Callers never specify a write offset and cannot
// reposition the handle: every [Handle.Write] is placed at the server-side
// allocated offset plus the number of bytes already accepted. Short writes are
// legal; retrying Write resumes at the first unwritten byte.
//
// Only the contiguous committed prefix is visible through [Volume.ReadAt] and
// [Volume.Size]. When a reservation is canceled before commit, the full
// reserved range is turned into an explicit hole (deallocated on platforms
// that support it, zero-filled otherwise) instead of being recycled for
// another request, so allocated offsets remain stable forever.
package appendhandle

import (
	"context"
	"errors"
	"io"
	"sync"
)

var (
	// ErrInvalidSize is returned when a reservation size is not positive.
	ErrInvalidSize = errors.New("appendhandle: reservation size must be positive")
	// ErrNegativeOffset is returned when a read is requested at a negative offset.
	ErrNegativeOffset = errors.New("appendhandle: negative read offset")
	// ErrHandleDone is returned when a handle is used after commit or abort.
	ErrHandleDone = errors.New("appendhandle: handle is already committed or aborted")
	// ErrReservationFull is returned when a write is attempted after every
	// reserved byte has already been accepted.
	ErrReservationFull = errors.New("appendhandle: reservation is fully written")
	// ErrPartialReservation is returned when commit is attempted before every
	// reserved byte has been written.
	ErrPartialReservation = errors.New("appendhandle: cannot commit a partially written reservation")
	// ErrIllegalWriteCount is returned by a storage implementation that reports
	// more accepted bytes than the buffer it was given.
	ErrIllegalWriteCount = errors.New("appendhandle: storage reported more bytes than requested")
)

// File is the storage contract required by [Volume]. *os.File is adapted
// through [OSFile]; test doubles may implement it directly.
type File interface {
	io.ReaderAt
	io.WriterAt
	// Truncate grows or shrinks the storage to the given absolute size.
	Truncate(size int64) error
	// Sync flushes prior writes and hole punches to stable storage.
	Sync() error
	// PunchHole deallocates [off, off+length). Reads of the range must
	// subsequently return zero bytes without changing the file size.
	PunchHole(off, length int64) error
}

// Options configures [Open].
type Options struct {
	// SyncOnCommit makes each prefix advancement call File.Sync. Defaults to
	// true, which is the safe choice for audit data.
	SyncOnCommit bool
	// InitialSize is the already-committed prefix length when reopening an
	// existing volume. It must not exceed the current storage size. Zero means
	// a fresh volume; [OpenVolume] fills this in from the file stat.
	InitialSize int64
}

const (
	statusOpen = iota
	statusCommitted
	statusAborted
)

// Volume serializes offset allocation and tracks the committed prefix.
type Volume struct {
	f File

	mu        sync.Mutex
	cond      *sync.Cond
	allocated int64
	committed int64
	pending   map[int64]*Handle

	syncOnCommit bool
}

// Open wraps an existing storage file. A nil Options means SyncOnCommit=true
// and InitialSize=0.
func Open(f File, opts *Options) (*Volume, error) {
	if f == nil {
		return nil, errors.New("appendhandle: nil storage file")
	}
	o := Options{SyncOnCommit: true}
	if opts != nil {
		o = *opts
	}
	if o.InitialSize < 0 {
		return nil, ErrNegativeOffset
	}
	v := &Volume{
		f:            f,
		pending:      make(map[int64]*Handle),
		allocated:    o.InitialSize,
		committed:    o.InitialSize,
		syncOnCommit: o.SyncOnCommit,
	}
	v.cond = sync.NewCond(&v.mu)
	return v, nil
}

// Reserve atomically allocates the next size bytes of the volume and returns
// a handle bound to that exclusive range. The storage is grown immediately,
// but the range stays invisible until the handle is committed (or aborted, in
// which case it becomes an explicit zero hole).
func (v *Volume) Reserve(size int64) (*Handle, error) {
	if size <= 0 {
		return nil, ErrInvalidSize
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	off := v.allocated
	if err := v.f.Truncate(off + size); err != nil {
		return nil, err
	}
	v.allocated = off + size

	h := &Handle{
		v:     v,
		off:   off,
		end:   off + size,
		state: statusOpen,
	}
	v.pending[off] = h
	return h, nil
}

// Size is the length of the contiguous committed prefix. Bytes beyond Size,
// including in-flight reservations, are not readable through the volume.
func (v *Volume) Size() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.committed
}

// Allocated is the high-water mark: every byte offset ever reserved, whether
// committed, in flight, or turned into a hole by an abort.
func (v *Volume) Allocated() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.allocated
}

// ReadAt implements io.ReaderAt over the committed prefix. Reads crossing the
// prefix are clipped and return io.EOF for the unreadable tail.
func (v *Volume) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrNegativeOffset
	}
	v.mu.Lock()
	limit := v.committed
	v.mu.Unlock()

	if off >= limit {
		return 0, io.EOF
	}
	clip := false
	if int64(len(p)) > limit-off {
		p = p[:limit-off]
		clip = true
	}
	n, err := v.f.ReadAt(p, off)
	if err == nil && clip {
		err = io.EOF
	}
	return n, err
}

// pumpLocked advances the committed prefix across every terminal handle at
// the current prefix boundary. Callers must hold v.mu.
func (v *Volume) pumpLocked() error {
	for {
		h, ok := v.pending[v.committed]
		if !ok || (h.state != statusCommitted && h.state != statusAborted) {
			return nil
		}
		if v.syncOnCommit {
			if err := v.f.Sync(); err != nil {
				return err
			}
		}
		end := h.end
		delete(v.pending, h.off)
		v.committed = end
		v.cond.Broadcast()
	}
}

// Handle is a single append reservation. It is safe for one Write/Commit/Abort
// sequence at a time; concurrent goroutines must not share a handle.
type Handle struct {
	v       *Volume
	off     int64
	end     int64
	written int64
	state   int

	mu sync.Mutex // guards written; state/offset/prefix are guarded by Volume.mu
}

// Offset is the server-allocated start offset of the reservation.
func (h *Handle) Offset() int64 { return h.off }

// ReservationSize is the number of bytes owned by the handle.
func (h *Handle) ReservationSize() int64 { return h.end - h.off }

// Written is the number of bytes accepted so far.
func (h *Handle) Written() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.written
}

// Done reports whether the handle has reached a terminal state.
func (h *Handle) Done() bool {
	h.v.mu.Lock()
	defer h.v.mu.Unlock()
	return h.state != statusOpen
}

// Write appends p at the internal write cursor. It never accepts an offset
// from the caller and never seeks. Short writes return the accepted byte
// count together with the storage error; calling Write again with the
// remaining bytes resumes exactly at the first unwritten byte.
//
// If ctx is canceled before the reservation is fully written, the handle is
// aborted automatically: the whole reservation is punched to an explicit
// hole and context.Cause(ctx) (or ctx.Err()) is returned. Plain storage
// errors do not abort the handle, so transient faults can be retried.
func (h *Handle) Write(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		_ = h.Abort()
		return 0, err
	}
	h.v.mu.Lock()
	if h.state != statusOpen {
		h.v.mu.Unlock()
		return 0, ErrHandleDone
	}
	h.v.mu.Unlock()

	accepted := 0
	for len(p) > 0 {
		if err := ctx.Err(); err != nil {
			_ = h.Abort()
			return accepted, err
		}

		h.mu.Lock()
		if h.written == h.end-h.off {
			h.mu.Unlock()
			return accepted, ErrReservationFull
		}
		target := h.off + h.written
		h.mu.Unlock()

		n, err := h.v.f.WriteAt(p, target)
		if n < 0 {
			return accepted, ErrIllegalWriteCount
		}
		if n > len(p) {
			return accepted, ErrIllegalWriteCount
		}
		h.mu.Lock()
		h.written += int64(n)
		h.mu.Unlock()
		accepted += n
		p = p[n:]
		if err != nil {
			return accepted, err
		}
	}
	return accepted, nil
}

// Commit publishes the reservation. It blocks while earlier reservations are
// still open so the committed prefix stays contiguous. A canceled context
// returns without changing state; Commit may be retried afterwards.
func (h *Handle) Commit(ctx context.Context) (int64, error) {
	v := h.v
	v.mu.Lock()
	if h.state == statusAborted {
		v.mu.Unlock()
		return 0, ErrHandleDone
	}
	if h.state == statusOpen {
		h.mu.Lock()
		written := h.written
		h.mu.Unlock()
		if written != h.end-h.off {
			v.mu.Unlock()
			return 0, ErrPartialReservation
		}
		h.state = statusCommitted
	}

	var wake sync.WaitGroup
	stop := make(chan struct{})
	if done := ctx.Done(); done != nil {
		wake.Add(1)
		go func() {
			defer wake.Done()
			select {
			case <-done:
				v.cond.Broadcast()
			case <-stop:
			}
		}()
	}

	defer v.mu.Unlock()
	defer close(stop)
	defer wake.Wait()
	for h.off >= v.committed {
		if err := v.pumpLocked(); err != nil {
			return v.committed, err
		}
		if h.off < v.committed {
			break
		}
		if err := ctx.Err(); err != nil {
			return v.committed, err
		}
		v.cond.Wait()
	}
	return v.committed, nil
}

// Abort cancels the reservation. The full reserved range, including bytes
// already written, is punched into an explicit hole and is never handed out
// again. When the aborted range reaches the prefix boundary the prefix
// advances over it, reading back as zeros.
func (h *Handle) Abort() error {
	v := h.v
	v.mu.Lock()
	defer v.mu.Unlock()
	if h.state == statusAborted {
		return nil
	}
	if h.state == statusCommitted {
		return ErrHandleDone
	}
	if err := h.v.f.PunchHole(h.off, h.end-h.off); err != nil {
		return err
	}
	h.state = statusAborted
	return v.pumpLocked()
}
