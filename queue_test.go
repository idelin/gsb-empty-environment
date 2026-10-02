package cbitmap

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestQueueBasicFIFO(t *testing.T) {
	q := NewQueue[int]()
	if !q.IsEmpty() {
		t.Fatal("new queue should be empty")
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("dequeue on empty queue should fail")
	}
	for i := 0; i < 1000; i++ {
		q.Enqueue(i)
	}
	if q.Len() != 1000 {
		t.Fatalf("len = %d, want 1000", q.Len())
	}
	for i := 0; i < 1000; i++ {
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("dequeue = %d,%v want %d,true", v, ok, i)
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("queue should be drained")
	}
	if !q.IsEmpty() {
		t.Fatal("queue should be empty after draining")
	}
}

func TestQueueInterleaved(t *testing.T) {
	q := NewQueue[int]()
	for i := 0; i < 500; i++ {
		q.Enqueue(i)
		v, ok := q.Dequeue()
		if !ok || v != i {
			t.Fatalf("interleaved enqueue/dequeue broke at %d", i)
		}
	}
}

// TestQueueMPMC hammers the queue with many producers and consumers and
// verifies that every enqueued value is dequeued exactly once.
func TestQueueMPMC(t *testing.T) {
	q := NewQueue[uint64]()
	const producers = 8
	const perProducer = 25_000
	const consumers = 8

	var wantSum uint64
	for p := 0; p < producers; p++ {
		for i := 1; i <= perProducer; i++ {
			wantSum += uint64(p*perProducer + i)
		}
	}

	var gotSum atomic.Uint64
	var gotCount atomic.Int64
	var wg sync.WaitGroup
	var done atomic.Int64

	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if done.Load() == producers && q.IsEmpty() {
					return
				}
				v, ok := q.Dequeue()
				if !ok {
					continue
				}
				gotSum.Add(v)
				gotCount.Add(1)
			}
		}()
	}
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 1; i <= perProducer; i++ {
				q.Enqueue(uint64(p*perProducer + i))
			}
			done.Add(1)
		}(p)
	}
	wg.Wait()

	if gotCount.Load() != producers*perProducer {
		t.Fatalf("dequeued %d items, want %d", gotCount.Load(), producers*perProducer)
	}
	if gotSum.Load() != wantSum {
		t.Fatalf("sum = %d, want %d (items lost or duplicated)", gotSum.Load(), wantSum)
	}
	if q.Len() != 0 {
		t.Fatalf("queue not empty at end: len=%d", q.Len())
	}
}

// TestQueueNoGoroutineLeak ensures queue usage spawns no lingering
// goroutines itself (the queue is passive; workers must exit).
func TestQueueNoGoroutineLeak(t *testing.T) {
	before := goroutineCount()
	var wg sync.WaitGroup
	q := NewQueue[int]()
	for w := 0; w < 32; w++ {
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
	if after := goroutineCount(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func BenchmarkQueueEnqueueDequeue(b *testing.B) {
	q := NewQueue[int]()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			q.Enqueue(1)
			q.Dequeue()
		}
	})
}
