package zerocache

import (
	"sync"
	"testing"
)

func TestPoolGetPut(t *testing.T) {
	p := NewSlicePool(4, 8)
	b := p.Get()
	if b == nil {
		t.Fatal("Get returned nil from non-empty pool")
	}
	if len(b) != 0 || cap(b) != 8 {
		t.Fatalf("buffer len/cap = %d/%d, want 0/8", len(b), cap(b))
	}
	if p.InUse() != 1 {
		t.Fatalf("InUse = %d, want 1", p.InUse())
	}
	p.Put(b)
	if p.InUse() != 0 {
		t.Fatalf("InUse = %d after Put, want 0", p.InUse())
	}
	if p.Available() != 4 {
		t.Fatalf("Available = %d, want 4", p.Available())
	}
}

func TestPoolExhaustion(t *testing.T) {
	p := NewSlicePool(2, 4)
	a := p.Get()
	b := p.Get()
	if a == nil || b == nil {
		t.Fatal("Get failed before exhaustion")
	}
	if c := p.Get(); c != nil {
		t.Fatalf("Get from exhausted pool = %v, want nil", c)
	}
	if p.InUse() != 2 {
		t.Fatalf("InUse = %d, want 2", p.InUse())
	}
	p.Put(a)
	if c := p.Get(); c == nil {
		t.Fatal("Get after Put returned nil")
	}
	p.Put(b)
}

func TestPoolPutForeignBufferDiscarded(t *testing.T) {
	p := NewSlicePool(1, 8)
	foreign := make([]uint64, 0, 16) // wrong capacity
	p.Put(foreign)
	_, _, discarded := p.Stats()
	if discarded != 1 {
		t.Fatalf("discarded = %d, want 1", discarded)
	}
	if p.Available() != 1 {
		t.Fatalf("foreign buffer entered free list")
	}
	p.Put(nil) // must not panic or count
	_, _, discarded = p.Stats()
	if discarded != 1 {
		t.Fatalf("nil Put changed counters")
	}
}

func TestPoolPutWhenFullDiscarded(t *testing.T) {
	p := NewSlicePool(1, 8)
	keep := p.Get()
	extra := p.Get() // nil, pool empty; make a real buffer manually
	if extra != nil {
		t.Fatal("expected nil from exhausted pool")
	}
	p.Put(keep) // pool full again
	// Simulate a buffer that was legitimately checked out earlier: get two.
	p2 := NewSlicePool(2, 8)
	x := p2.Get()
	y := p2.Get()
	p2.Put(x)
	p2.Put(y)
	// Double-returning y now overflows the pool and must be discarded.
	p2.Put(y)
	gets, puts, discarded := p2.Stats()
	if gets != 2 || puts != 2 || discarded != 1 {
		t.Fatalf("stats = %d/%d/%d, want 2/2/1", gets, puts, discarded)
	}
	if p2.InUse() != 0 {
		t.Fatalf("InUse = %d, want 0", p2.InUse())
	}
}

func TestPoolZeroAlloc(t *testing.T) {
	p := NewSlicePool(16, 64)
	b := p.Get()
	p.Put(b)
	allocs := testing.AllocsPerRun(1000, func() {
		b := p.Get()
		b = b[:cap(b)]
		b[0] = 1
		p.Put(b)
	})
	if allocs != 0 {
		t.Fatalf("Get/Put allocated %v times per run, want 0", allocs)
	}
}

func TestPoolConcurrentNoLeak(t *testing.T) {
	const (
		workers    = 16
		iterations = 5000
		capacity   = 32
	)
	p := NewSlicePool(capacity, 37)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				b := p.Get()
				if b == nil {
					continue // pool exhausted: nothing to return
				}
				b = b[:cap(b)]
				b[0] = seed + uint64(i)
				p.Put(b)
			}
		}(uint64(w))
	}
	wg.Wait()
	// Strict leak assertion: every successful Get was matched by a Put or a
	// discard, and nothing remains checked out.
	gets, puts, discarded := p.Stats()
	if gets != puts+discarded {
		t.Fatalf("leak: gets=%d puts=%d discarded=%d", gets, puts, discarded)
	}
	if p.InUse() != 0 {
		t.Fatalf("InUse = %d after drain, want 0", p.InUse())
	}
	if p.Available() != capacity {
		t.Fatalf("Available = %d, want %d (buffers lost)", p.Available(), capacity)
	}
}
