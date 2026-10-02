package hexgrid

import (
	"sync"
	"sync/atomic"
)

// RingBufferCap is the capacity of a pooled RingBuffer. A grid disk of
// radius k holds 1+3k(k+1) cells, so 128 supports k <= 6.
const RingBufferCap = 128

// RingBuffer is a fixed-capacity, reusable container for K-ring results.
// Because it is a plain array, using it through BufferPool performs zero
// heap allocations on the hot path.
type RingBuffer struct {
	Cells [RingBufferCap]Cell
	N     int
}

// Reset marks the buffer empty (O(1), memory is retained for reuse).
func (b *RingBuffer) Reset() { b.N = 0 }

// Slice returns the filled portion of the buffer.
func (b *RingBuffer) Slice() []Cell { return b.Cells[:b.N] }

// BufferPool is a goroutine-safe object pool of RingBuffers backed by
// sync.Pool. It tracks get/put counts so tests and operators can assert
// that nothing leaks.
type BufferPool struct {
	pool sync.Pool
	gets int64
	puts int64
}

// NewBufferPool creates a pool of ring buffers.
func NewBufferPool() *BufferPool {
	p := &BufferPool{}
	p.pool.New = func() interface{} { return &RingBuffer{} }
	return p
}

// Get acquires a buffer from the pool (allocating one only on a cold pool).
func (p *BufferPool) Get() *RingBuffer {
	atomic.AddInt64(&p.gets, 1)
	return p.pool.Get().(*RingBuffer)
}

// Put returns a buffer to the pool. The buffer is reset for the next user.
// A nil buffer is ignored (but still counted as returned).
func (p *BufferPool) Put(b *RingBuffer) {
	if b == nil {
		return
	}
	b.Reset()
	atomic.AddInt64(&p.puts, 1)
	p.pool.Put(b)
}

// Stats returns the cumulative number of Get and Put calls.
func (p *BufferPool) Stats() (gets, puts int64) {
	return atomic.LoadInt64(&p.gets), atomic.LoadInt64(&p.puts)
}

// Outstanding returns gets - puts; zero means nothing is leaked.
func (p *BufferPool) Outstanding() int64 {
	gets, puts := p.Stats()
	return gets - puts
}

// GridDiskBuffered fills buf with the grid disk of radius k around c and
// returns the result slice. ok is false when k is negative or the disk
// (1+3k(k+1) cells) exceeds the buffer capacity. Performs no allocations.
func GridDiskBuffered(c Cell, k int, buf *RingBuffer) ([]Cell, bool) {
	buf.Reset()
	if k < 0 || 1+3*k*(k+1) > RingBufferCap {
		return nil, false
	}
	s := GridDisk(c, k, buf.Cells[:0])
	buf.N = len(s)
	return s, true
}

// GridRingBuffered is the buffered variant of GridRing.
func GridRingBuffered(c Cell, k int, buf *RingBuffer) ([]Cell, bool) {
	buf.Reset()
	if k < 0 || 6*k > RingBufferCap {
		return nil, false
	}
	s := GridRing(c, k, buf.Cells[:0])
	buf.N = len(s)
	return s, true
}
