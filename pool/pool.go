// Package pool provides a size-classed, concurrency-safe byte-buffer pool
// with atomic acquire/release accounting. The accounting lets tests and
// production health checks assert that every acquired buffer is released
// exactly once (no leaks) with zero allocation on the hot path.
package pool

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Size classes: buffers are rounded up to the smallest class that fits.
// Requests larger than the largest class are allocated ad hoc and are not
// returned to any pool (they are tracked but dropped on release).
var classSizes = [...]int{64, 256, 1024, 4096, 16384, 65536}

const numClasses = len(classSizes)

// ErrDoubleRelease is returned by Release when a buffer that does not
// belong to this pool (or was already released) is handed back.
var ErrDoubleRelease = errors.New("pool: buffer not owned by this pool")

// Buffer is a leased byte slice. The zero-length slice has full class
// capacity; callers may reslice it up to cap().
type Buffer struct {
	data  []byte
	class int // -1 = oversized / ad hoc
	owner *BufferPool
	live  atomic.Bool // true while leased; guards against double release
}

// Bytes returns the leased slice.
func (b *Buffer) Bytes() []byte { return b.data }

// Cap returns the capacity of the leased slice.
func (b *Buffer) Cap() int { return cap(b.data) }

// Resize reslices the buffer to n bytes, growing the lease if needed.
// The returned Buffer replaces b; b must not be used afterwards.
func (b *Buffer) Resize(n int) *Buffer {
	if n <= cap(b.data) {
		b.data = b.data[:n]
		return b
	}
	nb := b.owner.Get(n)
	nb.data = nb.data[:n]
	copy(nb.data, b.data)
	_ = b.owner.Release(b)
	return nb
}

// BufferPool is a set of per-class sync.Pools with leak accounting.
type BufferPool struct {
	classes [numClasses]sync.Pool

	acquired atomic.Int64
	released atomic.Int64
	adhoc    atomic.Int64 // oversized allocations, never pooled
}

// NewBufferPool creates an empty pool.
func NewBufferPool() *BufferPool {
	p := &BufferPool{}
	for i, size := range classSizes {
		s := size
		p.classes[i].New = func() any {
			b := &Buffer{data: make([]byte, 0, s), class: i, owner: p}
			return b
		}
	}
	return p
}

func classFor(size int) int {
	for i, s := range classSizes {
		if size <= s {
			return i
		}
	}
	return -1
}

// Get leases a buffer with capacity for at least size bytes.
func (p *BufferPool) Get(size int) *Buffer {
	p.acquired.Add(1)
	cls := classFor(size)
	if cls < 0 {
		p.adhoc.Add(1)
		b := &Buffer{data: make([]byte, 0, size), class: -1, owner: p}
		b.live.Store(true)
		return b
	}
	b := p.classes[cls].Get().(*Buffer)
	b.data = b.data[:0]
	b.live.Store(true)
	return b
}

// Release returns a buffer to the pool. It returns ErrDoubleRelease if the
// buffer was not leased from this pool or was already released, so misuse
// is caught loudly in tests instead of corrupting the pool silently.
func (p *BufferPool) Release(b *Buffer) error {
	if b == nil || b.owner != p || !b.live.CompareAndSwap(true, false) {
		return ErrDoubleRelease
	}
	p.released.Add(1)
	if b.class < 0 {
		b.data = nil // oversized: drop, let GC reclaim
		return nil
	}
	b.data = b.data[:0]
	p.classes[b.class].Put(b)
	return nil
}

// Stats is a point-in-time snapshot of pool counters.
type Stats struct {
	Acquired int64
	Released int64
	AdHoc    int64
}

// InUse is Acquired - Released; a healthy process returns to 0 at quiesce.
func (s Stats) InUse() int64 { return s.Acquired - s.Released }

// Stats returns the current counters.
func (p *BufferPool) Stats() Stats {
	return Stats{
		Acquired: p.acquired.Load(),
		Released: p.released.Load(),
		AdHoc:    p.adhoc.Load(),
	}
}
