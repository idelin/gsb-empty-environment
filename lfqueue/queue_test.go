package lfqueue

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewInvalidCapacity(t *testing.T) {
	for _, cap := range []int{-1, 0, 1} {
		if _, err := New[int](cap); err == nil {
			t.Fatalf("New(%d) succeeded, want error", cap)
		}
	}
}

func TestCapacityRoundedToPowerOfTwo(t *testing.T) {
	q := MustNew[int](3)
	if q.Capacity() != 4 {
		t.Fatalf("Capacity = %d, want 4", q.Capacity())
	}
	q = MustNew[int](16)
	if q.Capacity() != 16 {
		t.Fatalf("Capacity = %d, want 16", q.Capacity())
	}
}

func TestDequeueEmpty(t *testing.T) {
	q := MustNew[int](8)
	if _, err := q.Dequeue(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Dequeue on empty = %v, want ErrEmpty", err)
	}
	if !q.Empty() || q.Len() != 0 {
		t.Fatalf("empty queue: Empty=%v Len=%d", q.Empty(), q.Len())
	}
}

func TestEnqueueFull(t *testing.T) {
	q := MustNew[int](4)
	for i := 0; i < 4; i++ {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("Enqueue %d failed: %v", i, err)
		}
	}
	if err := q.Enqueue(99); !errors.Is(err, ErrFull) {
		t.Fatalf("Enqueue on full = %v, want ErrFull", err)
	}
	if q.Len() != 4 {
		t.Fatalf("Len = %d, want 4", q.Len())
	}
}

func TestFIFOOrder(t *testing.T) {
	q := MustNew[string](8)
	for _, s := range []string{"a", "b", "c", "d"} {
		q.Enqueue(s)
	}
	for _, want := range []string{"a", "b", "c", "d"} {
		got, err := q.Dequeue()
		if err != nil || got != want {
			t.Fatalf("Dequeue = %q,%v want %q", got, err, want)
		}
	}
	if _, err := q.Dequeue(); !errors.Is(err, ErrEmpty) {
		t.Fatal("queue should be drained")
	}
}

func TestWraparound(t *testing.T) {
	q := MustNew[int](4)
	// Cycle the ring many times to exercise sequence-number wraparound.
	for lap := 0; lap < 100; lap++ {
		for i := 0; i < 4; i++ {
			if err := q.Enqueue(lap*10 + i); err != nil {
				t.Fatalf("lap %d enqueue %d: %v", lap, i, err)
			}
		}
		for i := 0; i < 4; i++ {
			got, err := q.Dequeue()
			if err != nil || got != lap*10+i {
				t.Fatalf("lap %d dequeue = %d,%v want %d", lap, got, err, lap*10+i)
			}
		}
	}
}

func TestInterleavedSingleThread(t *testing.T) {
	q := MustNew[int](8)
	for i := 0; i < 1000; i++ {
		q.Enqueue(i)
		q.Enqueue(-i)
		a, _ := q.Dequeue()
		if a != i {
			t.Fatalf("got %d want %d", a, i)
		}
		b, _ := q.Dequeue()
		if b != -i {
			t.Fatalf("got %d want %d", b, -i)
		}
	}
}

// TestMPMC is the core concurrency-safety check: many producers and
// consumers, every item delivered exactly once, FIFO per producer.
func TestMPMC(t *testing.T) {
	const (
		producers = 8
		consumers = 8
		perProd   = 20_000
	)
	total := producers * perProd
	q := MustNew[uint64](1024)

	seen := make([]atomic.Int32, total)
	var consumed atomic.Int64
	var wg sync.WaitGroup

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProd; i++ {
				v := uint64(p*perProd + i)
				for q.Enqueue(v) != nil {
					runtime.Gosched()
				}
			}
		}(p)
	}
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if consumed.Load() == int64(total) {
					return
				}
				v, err := q.Dequeue()
				if errors.Is(err, ErrEmpty) {
					runtime.Gosched()
					continue
				}
				if err != nil {
					t.Errorf("Dequeue: %v", err)
					return
				}
				if v >= uint64(total) {
					t.Errorf("value %d out of range", v)
					return
				}
				if seen[v].Add(1) != 1 {
					t.Errorf("value %d consumed twice", v)
					return
				}
				consumed.Add(1)
			}
		}()
	}
	wg.Wait()
	if consumed.Load() != int64(total) {
		t.Fatalf("consumed %d of %d items", consumed.Load(), total)
	}
	for i := 0; i < total; i++ {
		if seen[i].Load() != 1 {
			t.Fatalf("item %d seen %d times", i, seen[i].Load())
		}
	}
	if !q.Empty() {
		t.Fatalf("queue not drained, Len=%d", q.Len())
	}
}

// TestSPSC covers the single-producer/single-consumer fast path.
func TestSPSC(t *testing.T) {
	const n = 100_000
	q := MustNew[int](128)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			for q.Enqueue(i) != nil {
				runtime.Gosched()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; {
			v, err := q.Dequeue()
			if errors.Is(err, ErrEmpty) {
				runtime.Gosched()
				continue
			}
			if v != i {
				t.Errorf("order broken: got %d want %d", v, i)
				return
			}
			i++
		}
	}()
	wg.Wait()
}

// TestNoGoroutineLeak asserts the queue spawns no background goroutines
// and concurrent use leaves nothing behind.
func TestNoGoroutineLeak(t *testing.T) {
	base := goroutineCount(t)
	q := MustNew[int](64)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5_000; i++ {
				q.Enqueue(i)
				q.Dequeue()
			}
		}()
	}
	wg.Wait()
	if got := goroutineCount(t); got > base {
		t.Fatalf("goroutines leaked: before=%d after=%d", base, got)
	}
}

func goroutineCount(t *testing.T) int {
	t.Helper()
	// Let stragglers (GC workers etc.) settle.
	for i := 0; i < 50; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
		n := runtime.NumGoroutine()
		if i > 0 && n <= 2 {
			return n
		}
	}
	return runtime.NumGoroutine()
}

// mutexQueue is the naive coarse-locked baseline for benchmarks.
type mutexQueue struct {
	mu    sync.Mutex
	items []int
}

func (m *mutexQueue) Enqueue(v int) {
	m.mu.Lock()
	m.items = append(m.items, v)
	m.mu.Unlock()
}

func (m *mutexQueue) Dequeue() (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return 0, false
	}
	v := m.items[0]
	m.items = m.items[1:]
	return v, true
}

func BenchmarkLockFreeMPMC(b *testing.B) {
	q := MustNew[int](1024)
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if q.Enqueue(i) == nil {
				q.Dequeue()
			} else {
				q.Dequeue()
				q.Enqueue(i)
			}
			i++
		}
	})
}

func BenchmarkMutexQueue(b *testing.B) {
	m := &mutexQueue{}
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Enqueue(i)
			m.Dequeue()
			i++
		}
	})
}

func BenchmarkEnqueueDequeueSingle(b *testing.B) {
	q := MustNew[int](1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
		q.Dequeue()
	}
}
