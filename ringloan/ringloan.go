// Package ringloan implements a ring buffer that lends contiguous byte
// regions to consumers. A loan that crosses the ring tail is exposed as
// two segments. The producer cannot overwrite bytes pinned by an
// outstanding loan. Loans may be released out of order, but capacity is
// only reclaimed as a contiguous released prefix. Releasing the same
// loan twice is an error.
package ringloan

import (
	"context"
	"errors"
	"sync"
)

// ErrTooLarge is returned by Write when p can never fit in the buffer.
var ErrTooLarge = errors.New("ringloan: write larger than buffer capacity")

// ErrDoubleRelease is returned when a loan is released more than once.
var ErrDoubleRelease = errors.New("ringloan: loan already released")

// Buffer is a fixed-capacity ring buffer with consumer loans.
//
// Layout is tracked with absolute offsets: head is the oldest byte not
// yet reclaimed, lent is the end of the lent region, tail is the next
// write offset. Outstanding loans cover [head, lent) in lend order;
// readable-but-unlent bytes cover [lent, tail).
type Buffer struct {
	mu   sync.Mutex
	data []byte

	head uint64 // oldest unreclaimed byte (absolute)
	lent uint64 // end of lent region (absolute)
	tail uint64 // next write offset (absolute)

	loans []*Loan // outstanding loans, oldest first

	ch chan struct{} // closed on every state change to wake waiters
}

// New returns a Buffer with the given capacity in bytes.
func New(capacity int) *Buffer {
	if capacity <= 0 {
		panic("ringloan: capacity must be positive")
	}
	return &Buffer{
		data: make([]byte, capacity),
		ch:   make(chan struct{}),
	}
}

// Cap returns the total capacity of the buffer.
func (b *Buffer) Cap() int { return len(b.data) }

// Len returns the number of readable bytes (written, not yet reclaimed).
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.tail - b.head)
}

// Free returns the number of bytes the producer may still write.
// Bytes pinned by outstanding loans are not free.
func (b *Buffer) Free() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.data) - int(b.tail-b.head)
}

// broadcastLocked wakes every waiter; caller must hold b.mu.
func (b *Buffer) broadcastLocked() {
	close(b.ch)
	b.ch = make(chan struct{})
}

// Write copies p into the ring, blocking until all of p fits or ctx is
// done. It never performs a partial write: on success it returns
// len(p), otherwise 0 and the cause (ctx.Err() or ErrTooLarge).
func (b *Buffer) Write(ctx context.Context, p []byte) (int, error) {
	if len(p) > len(b.data) {
		return 0, ErrTooLarge
	}
	b.mu.Lock()
	for {
		if len(b.data)-int(b.tail-b.head) >= len(p) {
			b.writeLocked(p)
			b.mu.Unlock()
			return len(p), nil
		}
		ch := b.ch
		b.mu.Unlock()
		select {
		case <-ch:
			b.mu.Lock()
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// TryWrite copies p into the ring if it fits immediately and reports
// whether it did.
func (b *Buffer) TryWrite(p []byte) bool {
	if len(p) > len(b.data) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)-int(b.tail-b.head) < len(p) {
		return false
	}
	b.writeLocked(p)
	return true
}

// writeLocked copies p at the tail, wrapping around the ring end.
// Caller must hold b.mu and have verified the space is free.
func (b *Buffer) writeLocked(p []byte) {
	off := int(b.tail % uint64(len(b.data)))
	n := copy(b.data[off:], p)
	if n < len(p) {
		copy(b.data, p[n:])
	}
	b.tail += uint64(len(p))
	b.broadcastLocked()
}

// Lend borrows the n oldest bytes not already lent. It returns false
// (and a nil loan) if fewer than n bytes are available. The loan pins
// its bytes: the producer cannot overwrite them until the loan is
// released and every older loan has been released too.
func (b *Buffer) Lend(n int) (*Loan, bool) {
	if n <= 0 {
		panic("ringloan: loan size must be positive")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if int(b.tail-b.lent) < n {
		return nil, false
	}
	l := &Loan{
		buf:   b,
		start: b.lent,
		end:   b.lent + uint64(n),
	}
	b.lent += uint64(n)
	b.loans = append(b.loans, l)
	return l, true
}

// Loan is a contiguous region lent to a consumer. When the region
// wraps around the ring tail it is exposed as two segments.
type Loan struct {
	buf      *Buffer
	start    uint64 // absolute offset of first byte
	end      uint64 // absolute offset one past the last byte
	released bool
}

// Len returns the number of bytes in the loan.
func (l *Loan) Len() int { return int(l.end - l.start) }

// Segments returns the loan contents as one or two slices into the
// ring. The second slice is non-empty only when the loan wraps around
// the ring tail. The slices are valid until the loan is released and
// reclaimed; the caller must not mutate them.
func (l *Loan) Segments() (first, second []byte) {
	n := len(l.buf.data)
	off := int(l.start % uint64(n))
	end := off + int(l.end-l.start)
	if end <= n {
		return l.buf.data[off:end], nil
	}
	return l.buf.data[off:], l.buf.data[:end-n]
}

// Release marks the loan as consumed. Releases may happen in any
// order, but the buffer only reclaims capacity as a contiguous
// released prefix of the outstanding loans. Releasing a loan twice
// returns ErrDoubleRelease.
func (l *Loan) Release() error {
	b := l.buf
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.released {
		return ErrDoubleRelease
	}
	l.released = true
	// Reclaim the contiguous released prefix, advancing head.
	i := 0
	for i < len(b.loans) && b.loans[i].released {
		i++
	}
	if i > 0 {
		b.head = b.loans[i-1].end
		copy(b.loans, b.loans[i:])
		for j := len(b.loans) - i; j < len(b.loans); j++ {
			b.loans[j] = nil
		}
		b.loans = b.loans[:len(b.loans)-i]
		b.broadcastLocked()
	}
	return nil
}
