package lease

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrLeaseClosed is returned when reading through a lease whose owner
	// already released, canceled or timed out.
	ErrLeaseClosed = errors.New("lease: closed")
	// ErrSegmentRange is returned when a derived slice escapes the buffer.
	ErrSegmentRange = errors.New("lease: segment out of range")
)

// Lease owns one checked-out buffer. The owner starts with one reference;
// every derived Segment adds one. The buffer is zeroed and pooled again only
// after the last reference is dropped.
type Lease struct {
	pool *Pool
	buf  *buffer
	gen  uint64

	mu     sync.Mutex
	refs   int
	closed bool
	// zeroed runs the finalization exactly once no matter how release paths
	// (cancel, timeout, normal release) race each other.
	zeroed sync.Once
	// rootReleased prevents the owner's root reference from being dropped
	// twice; segment references are balanced in Segment.Release.
	rootReleased bool

	timeoutMu sync.Mutex
	timer     *time.Timer
}

func newLease(p *Pool, b *buffer, gen uint64) *Lease {
	return &Lease{pool: p, buf: b, gen: gen, refs: 1}
}

// Generation identifies the checkout during which this lease was issued.
func (l *Lease) Generation() uint64 { return l.gen }

// BufferID identifies the physical backing buffer.
func (l *Lease) BufferID() int { return l.buf.id }

// Write copies data into the backing buffer and returns a Segment the owner
// can lend downstream. The returned segment holds its own reference.
func (l *Lease) Write(offset int, data []byte) (*Segment, error) {
	if offset < 0 || offset+len(data) > len(l.buf.data) {
		return nil, ErrSegmentRange
	}
	l.mu.Lock()
	closed := l.closed
	if !closed {
		l.refs++
	}
	l.mu.Unlock()
	if closed {
		return nil, ErrLeaseClosed
	}
	copy(l.buf.data[offset:offset+len(data)], data)
	return l.segment(offset, len(data)), nil
}

// Segment derives a share-owned sub-slice over [offset, offset+length). It
// takes one reference so the buffer cannot be reused while the segment is
// outstanding.
func (l *Lease) Segment(offset, length int) (*Segment, error) {
	if offset < 0 || length < 0 || offset+length > len(l.buf.data) {
		return nil, ErrSegmentRange
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrLeaseClosed
	}
	l.refs++
	return l.segment(offset, length), nil
}

func (l *Lease) segment(offset, length int) *Segment {
	return &Segment{lease: l, off: offset, n: length}
}

// WithTimeout arms a deadline. Firing has the same effect as Cancel: the
// lease is marked closed immediately, while the buffer stays pinned and
// un-zeroed until every derived segment is released.
func (l *Lease) WithTimeout(d time.Duration) {
	l.timeoutMu.Lock()
	defer l.timeoutMu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
	}
	l.timer = time.AfterFunc(d, l.Cancel)
}

// Cancel terminates the lease. It is idempotent and safe to race with
// Release, timeout firing and Segment.Release.
func (l *Lease) Cancel() {
	l.markClosedAndDropRoot()
}

// Release terminates the lease and drops the owner's root reference. It is
// idempotent; the buffer is finalized only when the last reference goes.
func (l *Lease) Release() {
	l.markClosedAndDropRoot()
}

// Done reports whether the owner has terminated the lease. Outstanding
// segments may still keep the buffer pinned even when Done is true.
func (l *Lease) Done() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (l *Lease) markClosedAndDropRoot() {
	l.timeoutMu.Lock()
	if l.timer != nil {
		l.timer.Stop()
	}
	l.timeoutMu.Unlock()

	last := false
	l.mu.Lock()
	already := l.rootReleased
	l.closed = true
	if !already {
		l.rootReleased = true
		l.refs--
		last = l.refs == 0
	}
	l.mu.Unlock()
	if last {
		l.finalize()
	}
}

// dropSegment releases one derived-segment reference and finalizes when it
// was the last reference.
func (l *Lease) dropSegment() {
	l.mu.Lock()
	l.refs--
	last := l.refs == 0
	l.mu.Unlock()
	if last {
		l.finalize()
	}
}

// finalize zeroes the backing buffer and returns it to the pool. sync.Once
// guarantees the zeroing happens at most once under concurrent release paths.
func (l *Lease) finalize() {
	l.zeroed.Do(func() {
		for i := range l.buf.data {
			l.buf.data[i] = 0
		}
		l.pool.clears.Add(1)
		l.pool.put(l.buf)
	})
}

// Segment is a share-owned view into a leased buffer.
type Segment struct {
	lease   *Lease
	off, n  int
	relOnce sync.Once
}

// Bytes returns the segment payload. Read is rejected when:
//   - the segment was already released, or
//   - the lease owner released/canceled/timed out, or
//   - the physical buffer was reused under a newer generation (stale tenant).
func (s *Segment) Bytes() ([]byte, error) {
	l := s.lease
	l.mu.Lock()
	defer l.mu.Unlock()

	if s.n < 0 {
		return nil, ErrLeaseClosed
	}
	if l.closed {
		return nil, ErrLeaseClosed
	}
	if l.buf.gen != l.gen {
		return nil, ErrLeaseClosed
	}
	return l.buf.data[s.off : s.off+s.n], nil
}

// Release drops this segment's reference. Idempotent.
func (s *Segment) Release() {
	s.relOnce.Do(func() {
		s.lease.mu.Lock()
		s.n = -1
		s.lease.mu.Unlock()
		s.lease.dropSegment()
	})
}
