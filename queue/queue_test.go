package queue

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPanicsOnTinyCapacity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for capacity < 2")
		}
	}()
	New[int](1)
}

func TestCapacityRoundedToPowerOfTwo(t *testing.T) {
	q := New[int](3)
	if q.Cap() != 4 {
		t.Fatalf("Cap() = %d, want 4", q.Cap())
	}
	q = New[int](16)
	if q.Cap() != 16 {
		t.Fatalf("Cap() = %d, want 16", q.Cap())
	}
}

func TestDequeueEmpty(t *testing.T) {
	q := New[int](8)
	if _, err := q.Dequeue(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Dequeue on empty queue = %v, want ErrEmpty", err)
	}
	if q.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", q.Len())
	}
}

func TestEnqueueUntilFull(t *testing.T) {
	q := New[int](4)
	for i := 0; i < 4; i++ {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("Enqueue(%d) failed: %v", i, err)
		}
	}
	if err := q.Enqueue(99); !errors.Is(err, ErrFull) {
		t.Fatalf("Enqueue on full queue = %v, want ErrFull", err)
	}
	if q.Len() != 4 {
		t.Fatalf("Len() = %d, want 4", q.Len())
	}
}

func TestFIFOOrder(t *testing.T) {
	q := New[int](8)
	for i := 0; i < 8; i++ {
		q.MustEnqueue(i)
	}
	for i := 0; i < 8; i++ {
		v, err := q.Dequeue()
		if err != nil {
			t.Fatalf("Dequeue failed: %v", err)
		}
		if v != i {
			t.Fatalf("FIFO violated: got %d, want %d", v, i)
		}
	}
	if _, err := q.Dequeue(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("queue should be drained, got %v", err)
	}
}

// TestWraparound forces the ring to wrap many times, exercising the
// sequence-number recycling of slots.
func TestWraparound(t *testing.T) {
	const cap = 4
	q := New[int](cap)
	for round := 0; round < 1000; round++ {
		for i := 0; i < cap; i++ {
			if err := q.Enqueue(round*cap + i); err != nil {
				t.Fatalf("round %d enqueue %d: %v", round, i, err)
			}
		}
		if err := q.Enqueue(-1); !errors.Is(err, ErrFull) {
			t.Fatalf("round %d: expected ErrFull, got %v", round, err)
		}
		for i := 0; i < cap; i++ {
			v, err := q.Dequeue()
			if err != nil {
				t.Fatalf("round %d dequeue %d: %v", round, i, err)
			}
			if want := round*cap + i; v != want {
				t.Fatalf("round %d: got %d, want %d", round, v, want)
			}
		}
		if _, err := q.Dequeue(); !errors.Is(err, ErrEmpty) {
			t.Fatalf("round %d: expected ErrEmpty, got %v", round, err)
		}
	}
}

// TestConcurrentMPMC hammers the queue with many producers and consumers
// and verifies the fundamental invariant: every enqueued item is dequeued
// exactly once, with no loss, duplication, or corruption. Run with -race.
func TestConcurrentMPMC(t *testing.T) {
	const (
		producers    = 8
		consumers    = 8
		itemsPerProd = 20000
		total        = producers * itemsPerProd
	)
	q := New[uint64](1024)

	var consumed [total]atomic.Uint32
	var dequeued atomic.Uint64
	var wg sync.WaitGroup

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < itemsPerProd; i++ {
				v := uint64(p*itemsPerProd + i)
				for q.Enqueue(v) != nil {
					runtime.Gosched() // full: yield and retry
				}
			}
		}(p)
	}

	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				done := dequeued.Load()
				if done >= total {
					return
				}
				v, err := q.Dequeue()
				if err != nil {
					runtime.Gosched() // empty: yield and retry
					continue
				}
				if v >= total {
					t.Errorf("dequeued out-of-range value %d", v)
					return
				}
				if n := consumed[v].Add(1); n != 1 {
					t.Errorf("value %d consumed %d times", v, n)
					return
				}
				dequeued.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := dequeued.Load(); got != total {
		t.Fatalf("dequeued %d items, want %d", got, total)
	}
	for i := 0; i < total; i++ {
		if n := consumed[i].Load(); n != 1 {
			t.Fatalf("item %d consumed %d times, want exactly 1", i, n)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("queue not drained, Len() = %d", q.Len())
	}
}

// TestConcurrentSingleProducerSingleConsumer checks FIFO preservation with
// one producer and one consumer (FIFO order is observable in this case).
func TestConcurrentSPSCFIFO(t *testing.T) {
	const n = 100000
	q := New[int](128)
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
		for want := 0; want < n; {
			v, err := q.Dequeue()
			if err != nil {
				runtime.Gosched()
				continue
			}
			if v != want {
				t.Errorf("FIFO violated: got %d, want %d", v, want)
				return
			}
			want++
		}
	}()
	wg.Wait()
}

// TestNoGoroutineLeak proves the queue itself spawns nothing and that a
// produce/consume cycle leaves no goroutines behind.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	q := New[int](64)
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				for q.Enqueue(i) != nil {
					runtime.Gosched()
				}
			}
		}()
	}
	var drained atomic.Int64
	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for drained.Load() < 4000 {
				if _, err := q.Dequeue(); err == nil {
					drained.Add(1)
				} else {
					runtime.Gosched()
				}
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

func BenchmarkMPMC(b *testing.B) {
	q := New[int](1024)
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if err := q.Enqueue(i); err != nil {
				q.Dequeue()
				q.Enqueue(i)
			} else {
				q.Dequeue()
			}
			i++
		}
	})
}
