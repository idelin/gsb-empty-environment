package wirebuf

import (
	"sync"
	"sync/atomic"
)

// Stats tracks pool acquire/release balance for leak detection.
// All counters are updated atomically and safe to read concurrently.
var Stats struct {
	ReaderAcquire  int64
	ReaderRelease  int64
	MessageAcquire int64
	MessageRelease int64
	U64Acquire     int64
	U64Release     int64
	BufferAcquire  int64
	BufferRelease  int64
}

func add(p *int64, d int64) { atomic.AddInt64(p, d) }
func load(p *int64) int64   { return atomic.LoadInt64(p) }

// Balanced reports whether every acquired pooled object has been
// released. Tests assert this to prove the absence of leaks.
func Balanced() bool {
	return load(&Stats.ReaderAcquire) == load(&Stats.ReaderRelease) &&
		load(&Stats.MessageAcquire) == load(&Stats.MessageRelease) &&
		load(&Stats.U64Acquire) == load(&Stats.U64Release) &&
		load(&Stats.BufferAcquire) == load(&Stats.BufferRelease)
}

var readerPool = sync.Pool{New: func() interface{} { return &Reader{} }}

// AcquireReader returns a pooled Reader bound to buf.
func AcquireReader(buf []byte) *Reader {
	add(&Stats.ReaderAcquire, 1)
	r := readerPool.Get().(*Reader)
	r.Reset(buf)
	return r
}

// Release returns the Reader to the pool. The Reader must not be
// used afterwards.
func (r *Reader) Release() {
	r.buf = nil
	r.pos = 0
	readerPool.Put(r)
	add(&Stats.ReaderRelease, 1)
}

// U64s is a pooled, growable uint64 vector. The pointer indirection
// keeps sync.Pool Put/Get allocation-free (a bare slice would be
// boxed into an interface on every Put).
type U64s struct {
	V []uint64
}

var u64Pool = sync.Pool{New: func() interface{} {
	return &U64s{V: make([]uint64, 0, 16)}
}}

// AcquireU64s returns a pooled zero-length uint64 vector, typically
// used as the decode target for packed repeated fields.
func AcquireU64s() *U64s {
	add(&Stats.U64Acquire, 1)
	u := u64Pool.Get().(*U64s)
	u.V = u.V[:0]
	return u
}

// ReleaseU64s returns a vector obtained via AcquireU64s to the pool.
func ReleaseU64s(u *U64s) {
	if u == nil {
		return
	}
	u.V = u.V[:0]
	u64Pool.Put(u)
	add(&Stats.U64Release, 1)
}

// Buffer pool size classes: 2^minShift .. 2^maxShift bytes.
const (
	minShift   = 4  // 16 B
	maxShift   = 20 // 1 MiB
	numClasses = maxShift - minShift + 1
)

// BufferPool hands out recycled byte slices in power-of-two size
// classes. Get/Put are allocation-free on the hit path and safe for
// concurrent use.
type BufferPool struct {
	pools [numClasses]sync.Pool
}

// classFor maps a requested size to its size-class index. The second
// return value is false when size exceeds the largest class.
func classFor(size int) (int, bool) {
	if size <= 0 {
		return 0, true
	}
	if size > 1<<maxShift {
		return 0, false
	}
	// Smallest class c with 1<<(c+minShift) >= size.
	c := 0
	for capOf(c) < size {
		c++
	}
	return c, true
}

func capOf(class int) int { return 1 << (uint(class) + minShift) }

// Get returns a byte slice of at least size bytes (length == size).
// Oversized requests fall back to a fresh allocation and are not
// pooled on Put.
func (p *BufferPool) Get(size int) []byte {
	class, ok := classFor(size)
	if !ok {
		return make([]byte, size)
	}
	add(&Stats.BufferAcquire, 1)
	if v := p.pools[class].Get(); v != nil {
		return v.([]byte)[:size]
	}
	return make([]byte, size, capOf(class))
}

// Put recycles a buffer previously returned by Get. Buffers whose
// capacity does not match a size class exactly (e.g. the oversized
// fallback) are dropped for the GC.
func (p *BufferPool) Put(b []byte) {
	if b == nil {
		return
	}
	c := cap(b)
	class, ok := classFor(c)
	if !ok || capOf(class) != c {
		return
	}
	p.pools[class].Put(b[:0])
	add(&Stats.BufferRelease, 1)
}
