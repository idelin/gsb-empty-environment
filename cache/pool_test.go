package cache

import (
	"runtime/debug"
	"sync"
	"testing"
)

type bufObj struct {
	buf []byte
	n   int
}

func newBufPool() *Pool[bufObj] {
	return NewPool(
		func() *bufObj { return &bufObj{buf: make([]byte, 256)} },
		func(o *bufObj) {
			o.n = 0
			for i := range o.buf {
				o.buf[i] = 0
			}
		},
	)
}

func TestPoolGetPutReuse(t *testing.T) {
	p := newBufPool()
	a := p.Get()
	if p.InUse() != 1 {
		t.Fatalf("InUse=%d, want 1", p.InUse())
	}
	p.Put(a)
	if p.InUse() != 0 {
		t.Fatalf("InUse=%d after Put, want 0 (leak!)", p.InUse())
	}
	b := p.Get()
	if a != b {
		t.Fatal("expected object reuse from the pool")
	}
	p.Put(b)
	if got := p.Allocated(); got != 1 {
		t.Fatalf("Allocated=%d, want 1 (reuse must avoid new allocations)", got)
	}
}

func TestPoolResetOnPut(t *testing.T) {
	p := newBufPool()
	o := p.Get()
	o.n = 99
	o.buf[0] = 0xFF
	p.Put(o)
	o2 := p.Get()
	if o2.n != 0 || o2.buf[0] != 0 {
		t.Fatal("reset did not run on Put: state leaked between users")
	}
	p.Put(o2)
}

func TestPoolPutNilIgnored(t *testing.T) {
	p := newBufPool()
	p.Put(nil) // must not panic or corrupt counters
	if p.InUse() != 0 {
		t.Fatalf("InUse=%d after Put(nil), want 0", p.InUse())
	}
}

// TestPoolNoLeak is the strict leak assertion: after any balanced
// Get/Put sequence the outstanding-object counter must return to zero.
func TestPoolNoLeak(t *testing.T) {
	p := newBufPool()
	const batch = 100
	objs := make([]*bufObj, 0, batch)
	for i := 0; i < batch; i++ {
		objs = append(objs, p.Get())
	}
	if p.InUse() != batch {
		t.Fatalf("InUse=%d, want %d", p.InUse(), batch)
	}
	for _, o := range objs {
		p.Put(o)
	}
	if p.InUse() != 0 {
		t.Fatalf("LEAK: InUse=%d after returning all objects", p.InUse())
	}
}

// TestPoolConcurrent hammers the pool from many goroutines and asserts
// both race safety and the no-leak invariant.
func TestPoolConcurrent(t *testing.T) {
	p := newBufPool()
	const workers = 16
	const iters = 5000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				o := p.Get()
				o.n = i // touch the object so races would be caught by -race
				o.buf[0] = byte(i)
				p.Put(o)
			}
		}()
	}
	wg.Wait()
	if p.InUse() != 0 {
		t.Fatalf("LEAK: InUse=%d after concurrent balanced use", p.InUse())
	}
	if p.Allocated() == 0 {
		t.Fatal("pool never allocated")
	}
}

// TestPoolZeroAlloc proves Get/Put on a warm pool is allocation-free.
func TestPoolZeroAlloc(t *testing.T) {
	p := newBufPool()
	o := p.Get()
	p.Put(o) // warm up
	// Pin the pooled object so the GC cannot collect it mid-measurement.
	debug.SetGCPercent(-1)
	defer debug.SetGCPercent(100)
	allocs := testing.AllocsPerRun(1000, func() {
		v := p.Get()
		p.Put(v)
	})
	if allocs != 0 {
		t.Fatalf("pool Get/Put allocated %v times per op, want 0", allocs)
	}
}
