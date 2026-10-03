package corochan

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestRingQueueBasic(t *testing.T) {
	q := NewRingQueue[int](4) // capacity rounds up to 4
	if q.Cap() != 4 {
		t.Fatalf("Cap = %d, want 4", q.Cap())
	}
	if _, err := q.Dequeue(); err != ErrQueueEmpty {
		t.Fatalf("Dequeue on empty queue: err = %v, want ErrQueueEmpty", err)
	}
	for i := 0; i < 4; i++ {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("Enqueue(%d): %v", i, err)
		}
	}
	if err := q.Enqueue(99); err != ErrQueueFull {
		t.Fatalf("Enqueue on full queue: err = %v, want ErrQueueFull", err)
	}
	for i := 0; i < 4; i++ {
		v, err := q.Dequeue()
		if err != nil || v != i {
			t.Fatalf("Dequeue = (%v, %v), want (%d, nil)", v, err, i)
		}
	}
	if _, err := q.Dequeue(); err != ErrQueueEmpty {
		t.Fatalf("Dequeue after drain: err = %v, want ErrQueueEmpty", err)
	}
}

func TestRingQueueWraparound(t *testing.T) {
	q := NewRingQueue[int](2)
	for round := 0; round < 1000; round++ {
		if err := q.Enqueue(round); err != nil {
			t.Fatalf("round %d Enqueue: %v", round, err)
		}
		v, err := q.Dequeue()
		if err != nil || v != round {
			t.Fatalf("round %d Dequeue = (%v, %v)", round, v, err)
		}
	}
}

// MPMC stress: producers and consumers race through the lock-free queue;
// every enqueued value must be dequeued exactly once. Run with -race.
func TestRingQueueConcurrent(t *testing.T) {
	const producers, consumers, perProducer = 4, 4, 5000
	q := NewRingQueue[int](1024)

	var enqSum, deqSum atomic.Int64
	var deqCount atomic.Int64
	var wg sync.WaitGroup

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 1; i <= perProducer; i++ {
				v := base + i
				for q.Enqueue(v) == ErrQueueFull {
				}
				enqSum.Add(int64(v))
			}
		}(p * perProducer)
	}
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for deqCount.Load() < producers*perProducer {
				if v, err := q.Dequeue(); err == nil {
					deqSum.Add(int64(v))
					deqCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if deqCount.Load() != producers*perProducer {
		t.Fatalf("dequeued %d items, want %d", deqCount.Load(), producers*perProducer)
	}
	if enqSum.Load() != deqSum.Load() {
		t.Fatalf("sum mismatch: enqueued %d, dequeued %d", enqSum.Load(), deqSum.Load())
	}
}
