package queue

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// BoundedQueue boundary conditions
// ---------------------------------------------------------------------------

func TestBoundedEmptyDequeue(t *testing.T) {
	q := NewBounded(4)
	if _, ok := q.Dequeue(); ok {
		t.Fatal("dequeue from empty queue must fail")
	}
	if q.Len() != 0 {
		t.Fatalf("empty len = %d", q.Len())
	}
}

func TestBoundedCapacityRounding(t *testing.T) {
	for in, want := range map[int]int{1: 2, 2: 2, 3: 4, 5: 8, 16: 16, 17: 32} {
		if got := NewBounded(in).Capacity(); got != want {
			t.Fatalf("NewBounded(%d).Capacity() = %d, want %d", in, got, want)
		}
	}
}

func TestBoundedFullEnqueue(t *testing.T) {
	q := NewBounded(4)
	for i := 0; i < 4; i++ {
		if !q.Enqueue(i) {
			t.Fatalf("enqueue %d failed before capacity", i)
		}
	}
	if q.Enqueue(99) {
		t.Fatal("enqueue beyond capacity must fail")
	}
	if q.Len() != 4 {
		t.Fatalf("full len = %d", q.Len())
	}
}

func TestBoundedFIFOAndWraparound(t *testing.T) {
	q := NewBounded(4)
	// Force the ring to wrap several times.
	for round := 0; round < 100; round++ {
		for i := 0; i < 4; i++ {
			if !q.Enqueue(round*4 + i) {
				t.Fatalf("round %d enqueue %d failed", round, i)
			}
		}
		for i := 0; i < 4; i++ {
			v, ok := q.Dequeue()
			if !ok || v.(int) != round*4+i {
				t.Fatalf("round %d: got %v,%v want %d", round, v, ok, round*4+i)
			}
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("queue not drained")
	}
}

func TestBoundedInterleaved(t *testing.T) {
	q := NewBounded(2)
	if !q.Enqueue("a") || !q.Enqueue("b") {
		t.Fatal("initial enqueues failed")
	}
	if q.Enqueue("c") {
		t.Fatal("enqueue on full queue succeeded")
	}
	if v, _ := q.Dequeue(); v != "a" {
		t.Fatalf("got %v want a", v)
	}
	if !q.Enqueue("c") {
		t.Fatal("enqueue after dequeue failed")
	}
	if v, _ := q.Dequeue(); v != "b" {
		t.Fatalf("got %v want b", v)
	}
	if v, _ := q.Dequeue(); v != "c" {
		t.Fatalf("got %v want c", v)
	}
}

// ---------------------------------------------------------------------------
// Unbounded Queue boundary conditions
// ---------------------------------------------------------------------------

func TestUnboundedEmptyDequeue(t *testing.T) {
	q := New()
	if _, ok := q.Dequeue(); ok {
		t.Fatal("dequeue from empty queue must fail")
	}
	if q.Len() != 0 {
		t.Fatalf("empty len = %d", q.Len())
	}
}

func TestUnboundedFIFO(t *testing.T) {
	q := New()
	const n = 10000
	for i := 0; i < n; i++ {
		q.Enqueue(i)
	}
	if q.Len() != n {
		t.Fatalf("len = %d want %d", q.Len(), n)
	}
	for i := 0; i < n; i++ {
		v, ok := q.Dequeue()
		if !ok || v.(int) != i {
			t.Fatalf("got %v,%v want %d", v, ok, i)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("drained len = %d", q.Len())
	}
}

func TestUnboundedInterleaved(t *testing.T) {
	q := New()
	for i := 0; i < 1000; i++ {
		q.Enqueue(i)
		v, ok := q.Dequeue()
		if !ok || v.(int) != i {
			t.Fatalf("got %v,%v want %d", v, ok, i)
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrency: exactly-once delivery under MPMC contention
// ---------------------------------------------------------------------------

func runMPMC(t *testing.T, enqueue func(interface{}) bool, dequeue func() (interface{}, bool), producers, perProducer int) {
	t.Helper()
	total := producers * perProducer
	seen := make([]int32, total)
	var consumers sync.WaitGroup
	var producersWG sync.WaitGroup
	var done int32

	stop := make(chan struct{})
	consumer := func() {
		defer consumers.Done()
		for {
			v, ok := dequeue()
			if !ok {
				select {
				case <-stop:
					return
				default:
					runtime.Gosched()
					continue
				}
			}
			i := v.(int)
			if i < 0 || i >= total {
				t.Errorf("value out of range: %d", i)
				continue
			}
			if got := atomicAdd(&seen[i], 1); got != 1 {
				t.Errorf("value %d delivered %d times (not exactly-once)", i, got)
			}
			atomicAdd32(&done, 1)
		}
	}
	for c := 0; c < 4; c++ {
		consumers.Add(1)
		go consumer()
	}
	for p := 0; p < producers; p++ {
		producersWG.Add(1)
		go func(base int) {
			defer producersWG.Done()
			for i := 0; i < perProducer; i++ {
				for !enqueue(base + i) {
					runtime.Gosched() // retry while full
				}
			}
		}(p * perProducer)
	}
	producersWG.Wait()
	for atomicLoad32(&done) != int32(total) {
		runtime.Gosched()
	}
	close(stop)
	consumers.Wait()
	for i := range seen {
		if seen[i] != 1 {
			t.Fatalf("value %d seen %d times", i, seen[i])
		}
	}
}

func TestBoundedConcurrentMPMC(t *testing.T) {
	q := NewBounded(64)
	runMPMC(t,
		func(v interface{}) bool { return q.Enqueue(v) },
		func() (interface{}, bool) { return q.Dequeue() },
		8, 20000)
	if q.Len() != 0 {
		t.Fatalf("residual items: %d", q.Len())
	}
}

func TestUnboundedConcurrentMPMC(t *testing.T) {
	q := New()
	runMPMC(t,
		func(v interface{}) bool { q.Enqueue(v); return true },
		func() (interface{}, bool) { return q.Dequeue() },
		8, 20000)
	if q.Len() != 0 {
		t.Fatalf("residual items: %d", q.Len())
	}
}

// ---------------------------------------------------------------------------
// CAS counter
// ---------------------------------------------------------------------------

func TestCounterConcurrentAdd(t *testing.T) {
	var c Counter
	const goroutines, per = 16, 50000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				c.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := c.Value(); got != goroutines*per {
		t.Fatalf("counter = %d, want %d", got, goroutines*per)
	}
}

func TestCounterMax(t *testing.T) {
	var c Counter
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			for i := int64(0); i < 1000; i++ {
				c.Max(v*1000 + i)
			}
		}(int64(g))
	}
	wg.Wait()
	if got := c.Value(); got != 7*1000+999 {
		t.Fatalf("max = %d, want %d", got, 7999)
	}
}

// ---------------------------------------------------------------------------
// Goroutine-leak assertion: all helpers must terminate cleanly
// ---------------------------------------------------------------------------

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	q := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				q.Enqueue(i)
				q.Dequeue()
			}
		}()
	}
	wg.Wait()
	// Let any stragglers exit.
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		runtime.Gosched()
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// Tiny wrappers so tests read clearly under Go 1.11 (no atomic.Int32 type).
func atomicAdd(p *int32, d int32) int32   { return atomic.AddInt32(p, d) }
func atomicAdd32(p *int32, d int32) int32 { return atomic.AddInt32(p, d) }
func atomicLoad32(p *int32) int32         { return atomic.LoadInt32(p) }
