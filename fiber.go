package memsched

import "sync/atomic"

var nextFiberID int64

func newFiberID() int64 { return atomic.AddInt64(&nextFiberID, 1) }

// Fiber is a schedulable coroutine. All temporary objects it to be allocated
// by it are accounted against its ID in the Monitor. A fiber may carry a
// credit: memory the scheduler already reserved for it at admission time.
type Fiber struct {
	id        int64
	mon       *Monitor
	gcCapable bool
	credit    int64
}

func newFiber(mon *Monitor, id int64, gcCapable bool, credit int64) *Fiber {
	return &Fiber{id: id, mon: mon, gcCapable: gcCapable, credit: credit}
}

// ID returns the fiber's unique identifier.
func (f *Fiber) ID() int64 { return f.id }

// GCCapable reports whether the fiber can reclaim memory on demand and is
// therefore prioritized by the scheduler under memory pressure.
func (f *Fiber) GCCapable() bool { return f.gcCapable }

// Alloc accounts size units of temporary memory to this fiber. It first
// consumes the admission reservation (credit); any remainder blocks while
// the global quota is exhausted.
func (f *Fiber) Alloc(size int64) {
	if size <= 0 {
		return
	}
	if f.credit >= size {
		f.credit -= size
		return
	}
	rest := size - f.credit
	f.credit = 0
	f.mon.alloc(f.id, rest)
}

// Release frees size units of temporary memory previously allocated.
func (f *Fiber) Release(size int64) {
	if size <= 0 {
		return
	}
	f.mon.release(f.id, size)
}

// GC reclaims all temporary memory held by this fiber and returns the
// reclaimed amount. GC-capable fibers call this to relieve pressure.
func (f *Fiber) GC() int64 { return f.mon.releaseAll(f.id) }

// Usage returns the temp memory currently accounted to this fiber.
func (f *Fiber) Usage() int64 { return f.mon.FiberUsage(f.id) }
