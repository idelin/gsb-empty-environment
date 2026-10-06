// Package ringloan implements a fixed-capacity ring buffer for audio
// pipelines. The producer writes byte streams; consumers borrow contiguous
// ranges as loans. A loan that straddles the end of the ring is exposed as
// two segments. The producer may never overwrite bytes held by an
// outstanding loan. Loans may be released in any order, but capacity is
// returned to the producer only for the contiguous released prefix of the
// outstanding loan queue. Releasing the same loan twice is an error.
package ringloan

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrDoubleRelease is returned when a loan is released more than once.
	ErrDoubleRelease = errors.New("ringloan: loan already released")
	// ErrTooLarge is returned when a write or loan exceeds the capacity.
	ErrTooLarge = errors.New("ringloan: request exceeds buffer capacity")
	// ErrNotPositive is returned for non-positive loan sizes.
	ErrNotPositive = errors.New("ringloan: loan size must be positive")
)

// Buffer is a ring buffer with loan semantics. It is safe for concurrent
// use by one producer and any number of consumers.
type Buffer struct {
	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every state change

	buf     []byte
	base    uint64  // absolute position of the oldest byte occupying space
	loaned  uint64  // bytes below this position have been loaned out
	written uint64  // bytes below this position have been written
	loans   []*Loan // outstanding loans in issue order; loans[0] sits at base
}

// New returns a ring buffer with the given capacity in bytes.
func New(capacity int) *Buffer {
	if capacity <= 0 {
		panic("ringloan: capacity must be positive")
	}
	return &Buffer{buf: make([]byte, capacity), changed: make(chan struct{})}
}

// Capacity returns the total capacity of the ring in bytes.
func (b *Buffer) Capacity() int { return len(b.buf) }

// Free returns the bytes the producer may still write. Loaned-out bytes
// count as occupied until the contiguous released prefix covers them.
func (b *Buffer) Free() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf) - int(b.written-b.base)
}

// Available returns the written bytes not yet loaned to consumers.
func (b *Buffer) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.written - b.loaned)
}

func (b *Buffer) broadcastLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// Write blocks until all of p fits and copies it into the ring, wrapping
// across the end of the backing array as needed.
func (b *Buffer) Write(p []byte) error {
	return b.WriteContext(context.Background(), p)
}

// WriteContext is Write bounded by ctx. A cancelled wait leaves the ring
// untouched and returns ctx.Err().
func (b *Buffer) WriteContext(ctx context.Context, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if len(p) > len(b.buf) {
		return ErrTooLarge
	}
	b.mu.Lock()
	for {
		if len(b.buf)-int(b.written-b.base) >= len(p) {
			off := int(b.written % uint64(len(b.buf)))
			first := len(p)
			if rem := len(b.buf) - off; first > rem {
				first = rem
			}
			copy(b.buf[off:off+first], p[:first])
			copy(b.buf[:len(p)-first], p[first:])
			b.written += uint64(len(p))
			b.broadcastLocked()
			b.mu.Unlock()
			return nil
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
		b.mu.Lock()
	}
}

// Loan blocks until n bytes are available and lends them to the caller.
func (b *Buffer) Loan(n int) (*Loan, error) {
	return b.LoanContext(context.Background(), n)
}

// LoanContext is Loan bounded by ctx. A cancelled wait lends nothing and
// returns ctx.Err().
func (b *Buffer) LoanContext(ctx context.Context, n int) (*Loan, error) {
	if n <= 0 {
		return nil, ErrNotPositive
	}
	if n > len(b.buf) {
		return nil, ErrTooLarge
	}
	b.mu.Lock()
	for {
		if int(b.written-b.loaned) >= n {
			l := &Loan{buf: b, n: n}
			off := int(b.loaned % uint64(len(b.buf)))
			first := n
			if rem := len(b.buf) - off; first > rem {
				first = rem
			}
			l.First = b.buf[off : off+first]
			if n > first {
				l.Second = b.buf[:n-first]
			}
			b.loaned += uint64(n)
			b.loans = append(b.loans, l)
			b.mu.Unlock()
			return l, nil
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
		b.mu.Lock()
	}
}

func (b *Buffer) release(l *Loan) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.released {
		return ErrDoubleRelease
	}
	l.released = true
	// Capacity returns only for the contiguous released prefix: out-of-order
	// releases behind an unreleased loan mark state but do not advance base.
	prefix := 0
	for prefix < len(b.loans) && b.loans[prefix].released {
		b.base += uint64(b.loans[prefix].n)
		prefix++
	}
	if prefix > 0 {
		b.loans = append([]*Loan(nil), b.loans[prefix:]...)
		b.broadcastLocked()
	}
	return nil
}

// Loan is a contiguous range of the byte stream lent to a consumer. When
// the range straddles the end of the ring, First holds the tail segment and
// Second the wrapped head segment; otherwise Second is nil. The segments
// remain valid until Release.
type Loan struct {
	First  []byte
	Second []byte

	buf      *Buffer
	n        int
	released bool
}

// Len returns the total number of loaned bytes.
func (l *Loan) Len() int { return l.n }

// Release returns the loan. Releases may happen in any order; the ring
// reclaims the underlying capacity once every older loan is also released.
// Releasing twice returns ErrDoubleRelease.
func (l *Loan) Release() error {
	return l.buf.release(l)
}
