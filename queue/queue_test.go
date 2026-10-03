package queue

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestEmptyQueue(t *testing.T) {
	q := New[int]()
	if !q.Empty() {
		t.Fatal("new queue must be empty")
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("dequeue from empty queue must fail")
	}
	if q.Len() != 0 {
		t.Fatalf("len = %d, want 0", q.Len())
	}
}

func TestFIFOOrder(t *testing.T) {
	q := New[int]()
	const n = 1000
	for i := 0; i < n; i++ {
		q.Enqueue(i)
	}
	if q.Len() != n {
		t.Fatalf("len = %d, want %d", q.Len(), n)
	}
	for i := 0; i < n; i++ {
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("dequeue %d: got (%d,%v)", i, v, ok)
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("queue must be drained")
	}
	if !q.Empty() {
		t.Fatal("queue must report empty after drain")
	}
}

func TestInterleaved(t *testing.T) {
	q := New[string]()
	for round := 0; round < 100; round++ {
		q.Enqueue("a")
		q.Enqueue("b")
		v, ok := q.Dequeue()
		if !ok || v != "a" {
			t.Fatalf("round %d: got (%q,%v)", round, v, ok)
		}
		v, ok = q.Dequeue()
		if !ok || v != "b" {
			t.Fatalf("round %d: got (%q,%v)", round, v, ok)
		}
		if _, ok := q.Dequeue(); ok {
			t.Fatalf("round %d: queue should be empty", round)
		}
	}
}

// TestConcurrentMPMC hammers the queue with many producers and consumers
// and asserts the strict no-leak invariant: every enqueued item is
// dequeued exactly once, nothing is lost and nothing is duplicated.
func TestConcurrentMPMC(t *testing.T) {
	const (
		producers    = 8
		consumers    = 8
		itemsPerProd = 20000
		total        = producers * itemsPerProd
	)
	q := New[int]()

	var seen [total]atomic.Int32
	var consumed atomic.Int64
	var wg sync.WaitGroup

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < itemsPerProd; i++ {
				q.Enqueue(base + i)
			}
		}(p * itemsPerProd)
	}

	var stop atomic.Bool
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				v, ok := q.Dequeue()
				if !ok {
					continue
				}
				seen[v].Add(1)
				consumed.Add(1)
			}
		}()
	}

	// Wait for producers, then keep consuming until drained.
	done := make(chan struct{})
	go func() {
		for {
			if consumed.Load() == total {
				close(done)
				return
			}
		}
	}()
	<-done
	stop.Store(true)
	wg.Wait()

	// Drain anything left (should be nothing).
	for {
		v, ok := q.Dequeue()
		if !ok {
			break
		}
		seen[v].Add(1)
		consumed.Add(1)
	}

	if got := consumed.Load(); got != total {
		t.Fatalf("consumed = %d, want %d (leak or loss)", got, total)
	}
	duplicates, missing := 0, 0
	for i := 0; i < total; i++ {
		switch n := seen[i].Load(); {
		case n == 0:
			missing++
		case n > 1:
			duplicates++
		}
	}
	if missing != 0 || duplicates != 0 {
		t.Fatalf("missing=%d duplicates=%d", missing, duplicates)
	}
	if !q.Empty() || q.Len() != 0 {
		t.Fatalf("queue not empty at end: len=%d", q.Len())
	}
}

// TestConcurrentLenConsistency checks the atomic length counter under
// balanced concurrent load: after all workers join, Len must be exact.
func TestConcurrentLenConsistency(t *testing.T) {
	q := New[int]()
	const workers = 16
	const ops = 5000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func() { // producer
			defer wg.Done()
			for i := 0; i < ops; i++ {
				q.Enqueue(i)
			}
		}()
		go func() { // consumer
			defer wg.Done()
			left := ops
			for left > 0 {
				if _, ok := q.Dequeue(); ok {
					left--
				}
			}
		}()
	}
	wg.Wait()
	if n := q.Len(); n != 0 {
		t.Fatalf("len = %d after balanced load, want 0", n)
	}
	if !q.Empty() {
		t.Fatal("queue must be empty")
	}
}

// TestNoResidualNodes verifies that after draining, the internal list is
// back to a single dummy node — no chain of consumed nodes leaks.
func TestNoResidualNodes(t *testing.T) {
	q := New[int]()
	for i := 0; i < 100; i++ {
		q.Enqueue(i)
	}
	for i := 0; i < 100; i++ {
		q.Dequeue()
	}
	head := q.head.Load()
	if head.next.Load() != nil {
		t.Fatal("drained queue retains linked nodes")
	}
	if q.head.Load() != q.tail.Load() {
		t.Fatal("head and tail must coincide on the dummy node after drain")
	}
}

func BenchmarkEnqueueDequeue(b *testing.B) {
	q := New[int]()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			q.Enqueue(1)
			q.Dequeue()
		}
	})
}
