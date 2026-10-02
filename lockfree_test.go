package aries

import (
	"runtime"
	"sync"
	"testing"
)

// --- LockFreeQueue boundary conditions ---

func TestQueueEmptyDequeue(t *testing.T) {
	q := NewLockFreeQueue()
	if v, ok := q.Dequeue(); ok || v != nil {
		t.Fatalf("dequeue on empty queue = (%v,%v), want (nil,false)", v, ok)
	}
	if !q.IsEmpty() || q.Len() != 0 {
		t.Fatalf("empty queue Len=%d IsEmpty=%v", q.Len(), q.IsEmpty())
	}
}

func TestQueueSingleElement(t *testing.T) {
	q := NewLockFreeQueue()
	q.Enqueue("a")
	if q.Len() != 1 {
		t.Fatalf("Len=%d, want 1", q.Len())
	}
	v, ok := q.Dequeue()
	if !ok || v != "a" {
		t.Fatalf("dequeue = (%v,%v), want (a,true)", v, ok)
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("queue should be empty after draining")
	}
	if q.Len() != 0 || !q.IsEmpty() {
		t.Fatalf("after drain Len=%d IsEmpty=%v", q.Len(), q.IsEmpty())
	}
}

func TestQueueFIFOOrder(t *testing.T) {
	q := NewLockFreeQueue()
	const n = 1000
	for i := 0; i < n; i++ {
		q.Enqueue(i)
	}
	if q.Len() != n {
		t.Fatalf("Len=%d, want %d", q.Len(), n)
	}
	for i := 0; i < n; i++ {
		v, ok := q.Dequeue()
		if !ok || v.(int) != i {
			t.Fatalf("position %d: got (%v,%v)", i, v, ok)
		}
	}
	if !q.IsEmpty() {
		t.Fatal("queue not empty after draining; element leak")
	}
}

func TestQueueInterleavedEnqueueDequeue(t *testing.T) {
	q := NewLockFreeQueue()
	for i := 0; i < 500; i++ {
		q.Enqueue(i)
		v, ok := q.Dequeue()
		if !ok || v.(int) != i {
			t.Fatalf("iteration %d: got (%v,%v)", i, v, ok)
		}
		if q.Len() != 0 {
			t.Fatalf("iteration %d: Len=%d, want 0", i, q.Len())
		}
	}
}

// TestQueueConcurrentMPMC hammers the queue with many producers and
// consumers and asserts no element is lost, duplicated, or leaked.
func TestQueueConcurrentMPMC(t *testing.T) {
	q := NewLockFreeQueue()
	const producers = 8
	const consumers = 8
	const perProducer = 5000
	const total = producers * perProducer

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[int]int, total)
	consumed := 0

	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				v, ok := q.Dequeue()
				if !ok {
					mu.Lock()
					done := consumed >= total
					mu.Unlock()
					if done {
						return
					}
					runtime.Gosched()
					continue
				}
				mu.Lock()
				seen[v.(int)]++
				consumed++
				mu.Unlock()
			}
		}()
	}
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				q.Enqueue(base*perProducer + i)
			}
		}(p)
	}
	wg.Wait()

	if consumed != total {
		t.Fatalf("consumed %d elements, want %d", consumed, total)
	}
	for i := 0; i < total; i++ {
		if seen[i] != 1 {
			t.Fatalf("element %d seen %d times, want exactly 1", i, seen[i])
		}
	}
	// Strict no-leak: queue must be completely drained.
	if q.Len() != 0 || !q.IsEmpty() {
		t.Fatalf("leak: Len=%d after all consumed", q.Len())
	}
	if v, ok := q.Dequeue(); ok || v != nil {
		t.Fatalf("leak: residual element %v", v)
	}
}

// TestQueueConcurrentSPSC covers the single-producer/single-consumer
// fast path under concurrency.
func TestQueueConcurrentSPSC(t *testing.T) {
	q := NewLockFreeQueue()
	const n = 100000
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			q.Enqueue(i)
		}
		close(done)
	}()
	for i := 0; i < n; i++ {
		for {
			v, ok := q.Dequeue()
			if ok {
				if v.(int) != i {
					t.Fatalf("FIFO violated: got %v, want %d", v, i)
				}
				break
			}
			runtime.Gosched()
		}
	}
	<-done
	if !q.IsEmpty() {
		t.Fatal("leak: queue not empty")
	}
}

// --- LSNAllocator ---

func TestLSNAllocatorSequential(t *testing.T) {
	a := NewLSNAllocator(100)
	for want := uint64(101); want <= 110; want++ {
		if got := a.Next(); got != want {
			t.Fatalf("Next() = %d, want %d", got, want)
		}
	}
	if a.Current() != 110 {
		t.Fatalf("Current() = %d, want 110", a.Current())
	}
}

// TestLSNAllocatorConcurrent ensures CAS allocation never hands out
// the same LSN twice and never skips, under heavy contention.
func TestLSNAllocatorConcurrent(t *testing.T) {
	a := NewLSNAllocator(0)
	const goroutines = 16
	const perG = 10000
	const total = goroutines * perG
	seen := make([]uint32, total+1)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				lsn := a.Next()
				if lsn == 0 || lsn > total {
					t.Errorf("LSN %d out of range [1,%d]", lsn, total)
					return
				}
				seen[lsn]++
			}
		}()
	}
	wg.Wait()
	for lsn := uint64(1); lsn <= total; lsn++ {
		if seen[lsn] != 1 {
			t.Fatalf("LSN %d issued %d times, want exactly 1", lsn, seen[lsn])
		}
	}
	if a.Current() != total {
		t.Fatalf("Current() = %d, want %d", a.Current(), total)
	}
}
