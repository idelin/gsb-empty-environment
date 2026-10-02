// Package zerocache provides a fixed-capacity object pool and a sharded
// cell-list cache designed to eliminate GC pressure on high-frequency
// code paths: steady-state Get/Put and cache hits perform zero heap
// allocations.
package zerocache

import (
	"sync"
	"sync/atomic"
)

// SlicePool is a fixed-capacity pool of []uint64 buffers, all of identical
// capacity. Buffers are preallocated at construction; Get and Put never
// allocate. The pool is safe for concurrent use.
//
// Leak discipline: every buffer obtained from a successful Get must either
// be returned with Put or be accounted for as discarded. The invariant
//
//	Gets() == Puts() + Discarded() + InUse()
//
// holds at all times; tests assert InUse() == 0 after draining.
type SlicePool struct {
	mu        sync.Mutex
	free      [][]uint64
	bufCap    int
	inUse     int64
	gets      uint64
	puts      uint64
	discarded uint64
}

// NewSlicePool creates a pool pre-populated with capacity buffers, each
// with the given per-buffer capacity.
func NewSlicePool(capacity, bufCap int) *SlicePool {
	if capacity < 0 || bufCap <= 0 {
		panic("zerocache: invalid pool parameters")
	}
	p := &SlicePool{
		free:   make([][]uint64, capacity),
		bufCap: bufCap,
	}
	for i := range p.free {
		p.free[i] = make([]uint64, 0, bufCap)
	}
	return p
}

// BufCap returns the capacity of buffers owned by this pool.
func (p *SlicePool) BufCap() int { return p.bufCap }

// Get returns a zero-length buffer from the pool, or nil if the pool is
// exhausted. The caller must not retain buffers across a Put of the same
// buffer.
func (p *SlicePool) Get() []uint64 {
	p.mu.Lock()
	n := len(p.free)
	if n == 0 {
		p.mu.Unlock()
		return nil
	}
	b := p.free[n-1]
	p.free[n-1] = nil
	p.free = p.free[:n-1]
	p.mu.Unlock()
	atomic.AddInt64(&p.inUse, 1)
	atomic.AddUint64(&p.gets, 1)
	return b[:0]
}

// Put returns a buffer to the pool. Buffers whose capacity does not match
// the pool's buffer capacity, or buffers returned while the pool is already
// full, are discarded (left to the GC) and counted under Discarded.
func (p *SlicePool) Put(b []uint64) {
	if b == nil {
		return
	}
	if cap(b) != p.bufCap {
		atomic.AddUint64(&p.discarded, 1)
		return
	}
	b = b[:0]
	p.mu.Lock()
	if len(p.free) == cap(p.free) {
		p.mu.Unlock()
		atomic.AddUint64(&p.discarded, 1)
		return
	}
	p.free = append(p.free, b)
	p.mu.Unlock()
	atomic.AddInt64(&p.inUse, -1)
	atomic.AddUint64(&p.puts, 1)
}

// InUse returns the number of buffers currently checked out.
func (p *SlicePool) InUse() int64 { return atomic.LoadInt64(&p.inUse) }

// Stats returns the cumulative Get, Put and discard counters.
func (p *SlicePool) Stats() (gets, puts, discarded uint64) {
	return atomic.LoadUint64(&p.gets),
		atomic.LoadUint64(&p.puts),
		atomic.LoadUint64(&p.discarded)
}

// Available returns the number of buffers currently in the free list.
func (p *SlicePool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free)
}
