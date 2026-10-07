package boundedq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func mustEnqueue[T any](t *testing.T, q *Queue[T], v T) {
	t.Helper()
	if err := q.Enqueue(context.Background(), v); err != nil {
		t.Fatalf("Enqueue(%v) = %v, want nil", v, err)
	}
}

func mustDequeue[T any](t *testing.T, q *Queue[T]) T {
	t.Helper()
	v, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue() = %v, want nil", err)
	}
	return v
}

func TestFIFOOrder(t *testing.T) {
	q := New[int](3)
	for i := 0; i < 3; i++ {
		mustEnqueue(t, q, i)
	}
	for i := 0; i < 3; i++ {
		if got := mustDequeue(t, q); got != i {
			t.Fatalf("Dequeue() = %d, want %d", got, i)
		}
	}
}

func TestEnqueueBlocksWhenFull(t *testing.T) {
	q := New[int](1)
	mustEnqueue(t, q, 1)

	done := make(chan error, 1)
	go func() { done <- q.Enqueue(context.Background(), 2) }()

	select {
	case err := <-done:
		t.Fatalf("Enqueue on full queue returned %v, want blocked", err)
	case <-time.After(50 * time.Millisecond):
	}

	if got := mustDequeue(t, q); got != 1 {
		t.Fatalf("Dequeue() = %d, want 1", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("blocked Enqueue = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Enqueue did not complete after dequeue")
	}
	if got := mustDequeue(t, q); got != 2 {
		t.Fatalf("Dequeue() = %d, want 2", got)
	}
}

func TestEnqueueAfterCloseFails(t *testing.T) {
	q := New[int](2)
	q.Close()
	if err := q.Enqueue(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Enqueue after Close = %v, want ErrClosed", err)
	}
}

func TestCloseDrainsThenTerminates(t *testing.T) {
	q := New[int](3)
	mustEnqueue(t, q, 1)
	mustEnqueue(t, q, 2)
	q.Close()

	if got := mustDequeue(t, q); got != 1 {
		t.Fatalf("Dequeue() = %d, want 1", got)
	}
	if got := mustDequeue(t, q); got != 2 {
		t.Fatalf("Dequeue() = %d, want 2", got)
	}
	// Drained: every reader now observes termination.
	for i := 0; i < 3; i++ {
		if _, err := q.Dequeue(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("Dequeue after drain = %v, want ErrClosed", err)
		}
	}
}

func TestBlockedEnqueueWakesOnClose(t *testing.T) {
	q := New[int](1)
	mustEnqueue(t, q, 1)

	done := make(chan error, 1)
	go func() { done <- q.Enqueue(context.Background(), 2) }()
	time.Sleep(20 * time.Millisecond) // let the enqueue block

	q.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("blocked Enqueue after Close = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Enqueue not woken by Close")
	}
	// The committed element is still drainable.
	if got := mustDequeue(t, q); got != 1 {
		t.Fatalf("Dequeue() = %d, want 1", got)
	}
	if _, err := q.Dequeue(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Dequeue after drain = %v, want ErrClosed", err)
	}
}

func TestBlockedDequeueWakesOnClose(t *testing.T) {
	q := New[int](1)
	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(context.Background())
		done <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the dequeue block

	q.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("blocked Dequeue after Close = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Dequeue not woken by Close")
	}
}

func TestCancelledEnqueueKeepsCapacity(t *testing.T) {
	q := New[int](1)
	mustEnqueue(t, q, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Enqueue(ctx, 99) }()
	time.Sleep(20 * time.Millisecond) // let the enqueue block
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Enqueue = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled Enqueue did not return")
	}

	// The cancelled wait must not have swallowed capacity: after draining
	// one element, a fresh enqueue succeeds and the cancelled value is
	// nowhere in the stream.
	if got := mustDequeue(t, q); got != 1 {
		t.Fatalf("Dequeue() = %d, want 1", got)
	}
	mustEnqueue(t, q, 2)
	q.Close()
	if got := mustDequeue(t, q); got != 2 {
		t.Fatalf("Dequeue() = %d, want 2", got)
	}
	if _, err := q.Dequeue(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Dequeue after drain = %v, want ErrClosed", err)
	}
}

func TestCancelledDequeueKeepsElement(t *testing.T) {
	q := New[int](1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(ctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the dequeue block
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Dequeue = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled Dequeue did not return")
	}

	// The cancelled wait must not have swallowed the next element.
	mustEnqueue(t, q, 7)
	if got := mustDequeue(t, q); got != 7 {
		t.Fatalf("Dequeue() = %d, want 7", got)
	}
}

// TestConcurrentLinearizability hammers a small queue with concurrent
// producers, consumers and a closer, records the history, and verifies it
// against the sequential specification. Run with -race.
func TestConcurrentLinearizability(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		const capacity = 3
		q := New[int](capacity)
		var rec Recorder

		var wg sync.WaitGroup
		for p := 0; p < 3; p++ {
			wg.Add(1)
			go func(p int) {
				defer wg.Done()
				for i := 0; i < 6; i++ {
					v := p*1000 + i
					call := rec.Begin()
					err := q.Enqueue(context.Background(), v)
					rec.End(call, EnqueueOp, v, err)
					if errors.Is(err, ErrClosed) {
						return
					}
				}
			}(p)
		}
		for c := 0; c < 2; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					call := rec.Begin()
					v, err := q.Dequeue(context.Background())
					rec.End(call, DequeueOp, v, err)
					if errors.Is(err, ErrClosed) {
						return
					}
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			call := rec.Begin()
			q.Close()
			rec.End(call, CloseOp, nil, nil)
		}()
		wg.Wait()

		ops := rec.Ops()
		if _, ok := CheckLinearizable(capacity, ops); !ok {
			t.Fatalf("trial %d: history is not linearizable (%d ops)", trial, len(ops))
		}

		// Sanity: successfully dequeued values are exactly the committed
		// enqueued values (implied by linearizability, but cheap to check).
		var enq, deq []int
		for _, op := range ops {
			switch op.Kind {
			case EnqueueOp:
				if op.Err == nil {
					enq = append(enq, op.Value.(int))
				}
			case DequeueOp:
				if op.Err == nil {
					deq = append(deq, op.Value.(int))
				}
			}
		}
		// Drain whatever remains committed after close.
		for {
			v, err := q.Dequeue(context.Background())
			if errors.Is(err, ErrClosed) {
				break
			}
			if err != nil {
				t.Fatalf("trial %d: drain Dequeue = %v", trial, err)
			}
			deq = append(deq, v)
		}
		sort.Ints(enq)
		sort.Ints(deq)
		if fmt.Sprint(enq) != fmt.Sprint(deq) {
			t.Fatalf("trial %d: enqueued %v, dequeued %v", trial, enq, deq)
		}
	}
}
