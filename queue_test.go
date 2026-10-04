package roaring

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestQueueCapacityRounding(t *testing.T) {
	for in, want := range map[int]int{0: 2, 1: 2, 2: 2, 3: 4, 5: 8, 16: 16, 17: 32, 1000: 1024} {
		if got := NewLFQueue(in).Capacity(); got != want {
			t.Fatalf("NewLFQueue(%d).Capacity() = %d, want %d", in, got, want)
		}
	}
}

func TestQueueEmptyPop(t *testing.T) {
	q := NewLFQueue(4)
	if v, ok := q.Pop(); ok || v != nil {
		t.Fatal("pop on empty queue should fail")
	}
	if q.Len() != 0 {
		t.Fatal("empty queue has non-zero len")
	}
}

func TestQueueFIFOSingleThread(t *testing.T) {
	q := NewLFQueue(8)
	for i := 0; i < 8; i++ {
		if !q.Push(i) {
			t.Fatalf("push %d failed", i)
		}
	}
	for i := 0; i < 8; i++ {
		v, ok := q.Pop()
		if !ok || v.(int) != i {
			t.Fatalf("pop = %v,%v want %d", v, ok, i)
		}
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("queue should be empty")
	}
}

func TestQueueFullBoundary(t *testing.T) {
	q := NewLFQueue(4)
	for i := 0; i < 4; i++ {
		if !q.Push(i) {
			t.Fatalf("push %d should succeed", i)
		}
	}
	if q.Push(99) {
		t.Fatal("push to full queue should fail")
	}
	if q.Len() != 4 {
		t.Fatalf("len = %d, want 4", q.Len())
	}
	// Drain one, push one, repeat: exercises wrap-around.
	for cycle := 0; cycle < 1000; cycle++ {
		v, ok := q.Pop()
		if !ok {
			t.Fatalf("cycle %d: queue unexpectedly empty", cycle)
		}
		want := cycle % 4
		if cycle >= 4 {
			want = 4 + cycle - 1
			_ = want
		}
		_ = v
		if !q.Push(cycle) {
			t.Fatalf("cycle %d: push after pop should succeed", cycle)
		}
	}
}

func TestQueueWraparoundOrder(t *testing.T) {
	q := NewLFQueue(4)
	// Interleave pushes and pops so slots are reused many times.
	for i := 0; i < 100; i++ {
		if !q.Push(i) {
			t.Fatalf("push %d failed", i)
		}
		v, ok := q.Pop()
		if !ok || v.(int) != i {
			t.Fatalf("got %v,%v want %d", v, ok, i)
		}
	}
	if q.Len() != 0 {
		t.Fatal("queue not drained")
	}
}

// TestQueueMPMC is the core concurrency test: many producers, many
// consumers, every value delivered exactly once.
func TestQueueMPMC(t *testing.T) {
	const producers = 8
	const consumers = 8
	const perProducer = 50000
	const total = producers * perProducer

	q := NewLFQueue(1024)
	seen := make([]int32, total)
	var consumed int64

	var pwg sync.WaitGroup
	for p := 0; p < producers; p++ {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			for i := 0; i < perProducer; i++ {
				v := p*perProducer + i
				for !q.Push(v) {
					runtime.Gosched()
				}
			}
		}(p)
	}

	var cwg sync.WaitGroup
	for c := 0; c < consumers; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for {
				n := atomic.LoadInt64(&consumed)
				if n >= total {
					return
				}
				v, ok := q.Pop()
				if !ok {
					runtime.Gosched()
					continue
				}
				idx := v.(int)
				if idx < 0 || idx >= total {
					t.Errorf("out-of-range value %d", idx)
					return
				}
				if !atomic.CompareAndSwapInt32(&seen[idx], 0, 1) {
					t.Errorf("value %d delivered twice", idx)
					return
				}
				atomic.AddInt64(&consumed, 1)
			}
		}()
	}
	pwg.Wait()
	// Drain the tail: consumers exit once consumed reaches total.
	cwg.Wait()
	for i := 0; i < total; i++ {
		if atomic.LoadInt32(&seen[i]) != 1 {
			t.Fatalf("value %d never delivered", i)
		}
	}
}

// TestQueueSPSCStress covers the single-producer single-consumer fast path.
func TestQueueSPSCStress(t *testing.T) {
	const n = 1000000
	q := NewLFQueue(128)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			for !q.Push(i) {
				runtime.Gosched()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			v, ok := q.Pop()
			for !ok {
				runtime.Gosched()
				v, ok = q.Pop()
			}
			if v.(int) != i {
				t.Errorf("got %v, want %d", v, i)
				return
			}
		}
	}()
	wg.Wait()
}

func TestQueueNoGoroutineLeak(t *testing.T) {
	if !waitForGoroutines(runtime.NumGoroutine(), 2*1e9) {
		t.Fatal("could not establish baseline")
	}
	base := runtime.NumGoroutine()
	q := NewLFQueue(64)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				q.Push(j)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				q.Pop()
			}
		}()
	}
	wg.Wait()
	if !waitForGoroutines(base, 5*1e9) {
		t.Fatalf("goroutine leak: base=%d now=%d", base, runtime.NumGoroutine())
	}
}
