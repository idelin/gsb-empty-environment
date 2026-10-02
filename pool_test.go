package hexgrid

import (
	"runtime"
	"sync"
	"testing"
)

// sharedTestPool is used by the concurrency tests across files.
var sharedTestPool = NewBufferPool()

func TestPoolGetPutBalance(t *testing.T) {
	p := NewBufferPool()
	buf := p.Get()
	if buf == nil {
		t.Fatal("Get returned nil")
	}
	p.Put(buf)
	gets, puts := p.Stats()
	if gets != 1 || puts != 1 {
		t.Fatalf("stats gets=%d puts=%d, want 1/1", gets, puts)
	}
	if p.Outstanding() != 0 {
		t.Fatalf("outstanding = %d, want 0 (leak)", p.Outstanding())
	}
}

func TestPoolPutNilIgnored(t *testing.T) {
	p := NewBufferPool()
	p.Put(nil)
	gets, puts := p.Stats()
	if gets != 0 || puts != 0 {
		t.Fatalf("nil put must be ignored, got gets=%d puts=%d", gets, puts)
	}
}

func TestPoolBufferReuse(t *testing.T) {
	p := NewBufferPool()
	first := p.Get()
	p.Put(first)
	second := p.Get()
	if first != second {
		t.Fatal("expected buffer reuse from the pool")
	}
	p.Put(second)
}

func TestGridDiskBufferedCapacity(t *testing.T) {
	p := NewBufferPool()
	buf := p.Get()
	c := beijingCell()
	// k=6 -> 127 cells fits; k=7 -> 169 > RingBufferCap must fail cleanly.
	if disk, ok := GridDiskBuffered(c, 6, buf); !ok || len(disk) != 127 {
		t.Fatalf("k=6: ok=%v len=%d, want true/127", ok, len(disk))
	}
	if _, ok := GridDiskBuffered(c, 7, buf); ok {
		t.Fatal("k=7 must report capacity overflow")
	}
	if _, ok := GridDiskBuffered(c, -1, buf); ok {
		t.Fatal("negative k must fail")
	}
	// Failed calls leave the buffer empty.
	if buf.N != 0 {
		t.Fatalf("buffer not reset after failed call, N=%d", buf.N)
	}
	p.Put(buf)
}

// TestPoolZeroAlloc: the hot path (pool get -> buffered k-ring -> put)
// must perform zero heap allocations once the pool is warm.
func TestPoolZeroAlloc(t *testing.T) {
	p := NewBufferPool()
	c := beijingCell()
	buf := p.Get()
	p.Put(buf) // warm the pool
	allocs := testing.AllocsPerRun(200, func() {
		b := p.Get()
		if _, ok := GridDiskBuffered(c, 3, b); !ok {
			t.Fatal("disk failed")
		}
		p.Put(b)
	})
	if allocs != 0 {
		t.Fatalf("hot path allocated %v times per op, want 0", allocs)
	}
	if p.Outstanding() != 0 {
		t.Fatalf("outstanding = %d after alloc test, want 0", p.Outstanding())
	}
}

// TestPoolConcurrentNoLeak: many goroutines acquire/release buffers; the
// books must balance exactly (strict no-leak assertion) and no goroutine
// may be left behind.
func TestPoolConcurrentNoLeak(t *testing.T) {
	p := NewBufferPool()
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c := LatLngToCell(39.9+float64(id)*0.01, 116.4, 8)
			for i := 0; i < 5000; i++ {
				b := p.Get()
				if b == nil {
					t.Error("nil buffer")
					return
				}
				if _, ok := GridDiskBuffered(c, 3, b); !ok {
					t.Error("disk failed")
					p.Put(b)
					return
				}
				p.Put(b)
			}
		}(g)
	}
	wg.Wait()
	gets, puts := p.Stats()
	if gets != puts || gets != 16*5000 {
		t.Fatalf("leak: gets=%d puts=%d, want both %d", gets, puts, 16*5000)
	}
	if p.Outstanding() != 0 {
		t.Fatalf("outstanding = %d, want 0", p.Outstanding())
	}
	// No goroutine leaks from the pool machinery itself.
	for i := 0; i < 100 && runtime.NumGoroutine() > before; i++ {
		runtime.Gosched()
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestRingBufferReset: Reset keeps capacity for reuse (memory recycling).
func TestRingBufferReset(t *testing.T) {
	var b RingBuffer
	b.N = 42
	b.Reset()
	if b.N != 0 {
		t.Fatal("Reset must zero N")
	}
	if len(b.Slice()) != 0 {
		t.Fatal("Slice after reset must be empty")
	}
}
