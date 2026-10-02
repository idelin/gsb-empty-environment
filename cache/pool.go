package cache

import (
	"sync"
	"sync/atomic"
)

// Pool is a type-safe object pool built on sync.Pool. It recycles objects
// to eliminate allocation pressure and GC churn on high-frequency paths.
//
// Leak discipline: every Get must be balanced by exactly one Put. The
// InUse counter tracks outstanding objects so tests can assert a strict
// no-leak invariant (InUse() == 0 after balanced use). Putting the same
// object twice is a programming error, as with sync.Pool.
type Pool[T any] struct {
	pool      sync.Pool
	newFn     func() *T
	reset     func(*T)
	inUse     atomic.Int64
	allocated atomic.Int64
}

// NewPool creates a pool. newFn constructs fresh objects; reset (may be
// nil) sanitises an object before it is recycled so no state leaks
// between users.
func NewPool[T any](newFn func() *T, reset func(*T)) *Pool[T] {
	p := &Pool[T]{newFn: newFn, reset: reset}
	return p
}

// Get returns an object from the pool, allocating a new one if empty.
func (p *Pool[T]) Get() *T {
	if v := p.pool.Get(); v != nil {
		p.inUse.Add(1)
		return v.(*T)
	}
	p.allocated.Add(1)
	p.inUse.Add(1)
	return p.newFn()
}

// Put resets and recycles an object. A nil object is ignored.
func (p *Pool[T]) Put(v *T) {
	if v == nil {
		return
	}
	if p.reset != nil {
		p.reset(v)
	}
	p.inUse.Add(-1)
	p.pool.Put(v)
}

// InUse returns the number of objects currently checked out.
func (p *Pool[T]) InUse() int64 { return p.inUse.Load() }

// Allocated returns the total number of objects ever constructed.
func (p *Pool[T]) Allocated() int64 { return p.allocated.Load() }
