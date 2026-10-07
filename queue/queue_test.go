package queue_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"bq/lin"
	"bq/queue"
)

func statusOf(err error) lin.Status {
	switch {
	case err == nil:
		return lin.OK
	case errors.Is(err, queue.ErrClosed):
		return lin.Closed
	default:
		return lin.Canceled
	}
}

func TestFIFOOrder(t *testing.T) {
	q := queue.New[int](3)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, i); err != nil {
			t.Fatalf("Enqueue(%d): %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		v, err := q.Dequeue(ctx)
		if err != nil || v != i {
			t.Fatalf("Dequeue = %v, %v; want %d, nil", v, err, i)
		}
	}
}

func TestBlocksUntilSpaceAndItem(t *testing.T) {
	q := queue.New[int](1)
	ctx := context.Background()
	if err := q.Enqueue(ctx, 1); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := q.Enqueue(ctx, 2); err != nil {
			t.Errorf("blocked Enqueue: %v", err)
		}
	}()
	select {
	case <-done:
		t.Fatal("Enqueue returned while queue full")
	case <-time.After(20 * time.Millisecond):
	}
	if v, err := q.Dequeue(ctx); err != nil || v != 1 {
		t.Fatalf("Dequeue = %v, %v", v, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked Enqueue did not complete after Dequeue freed space")
	}
	if v, err := q.Dequeue(ctx); err != nil || v != 2 {
		t.Fatalf("Dequeue = %v, %v; want 2, nil", v, err)
	}
}

func TestCloseDrainsCommittedThenTerminates(t *testing.T) {
	q := queue.New[int](4)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	q.Close()
	for i := 0; i < 3; i++ {
		v, err := q.Dequeue(ctx)
		if err != nil || v != i {
			t.Fatalf("drain Dequeue = %v, %v; want %d, nil", v, err, i)
		}
	}
	if _, err := q.Dequeue(ctx); !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("Dequeue after drain = %v, want ErrClosed", err)
	}
	if err := q.Enqueue(ctx, 9); !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("Enqueue after close = %v, want ErrClosed", err)
	}
}

func TestCloseUnblocksWaiters(t *testing.T) {
	ctx := context.Background()

	// Blocked Dequeue terminates with ErrClosed.
	q := queue.New[int](1)
	deqDone := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(ctx)
		deqDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	q.Close()
	if err := <-deqDone; !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("blocked Dequeue got %v, want ErrClosed", err)
	}

	// Blocked Enqueue fails with ErrClosed and commits nothing.
	q2 := queue.New[int](1)
	if err := q2.Enqueue(ctx, 1); err != nil {
		t.Fatal(err)
	}
	enqDone := make(chan error, 1)
	go func() { enqDone <- q2.Enqueue(ctx, 2) }()
	time.Sleep(20 * time.Millisecond)
	q2.Close()
	if err := <-enqDone; !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("blocked Enqueue got %v, want ErrClosed", err)
	}
	if v, err := q2.Dequeue(ctx); err != nil || v != 1 {
		t.Fatalf("Dequeue = %v, %v; want 1, nil", v, err)
	}
	if _, err := q2.Dequeue(ctx); !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("Dequeue after drain = %v, want ErrClosed", err)
	}
}

func TestCancelDequeueKeepsItem(t *testing.T) {
	q := queue.New[int](1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(ctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Dequeue got %v, want context.Canceled", err)
	}
	// The cancelled wait must not have consumed anything.
	if err := q.Enqueue(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if v, err := q.Dequeue(context.Background()); err != nil || v != 42 {
		t.Fatalf("Dequeue = %v, %v; want 42, nil", v, err)
	}
}

func TestCancelEnqueueKeepsCapacity(t *testing.T) {
	q := queue.New[int](1)
	bg := context.Background()
	if err := q.Enqueue(bg, 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- q.Enqueue(ctx, 2) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Enqueue got %v, want context.Canceled", err)
	}
	// Capacity must be intact: drain one, the next Enqueue succeeds.
	if v, err := q.Dequeue(bg); err != nil || v != 1 {
		t.Fatalf("Dequeue = %v, %v; want 1, nil", v, err)
	}
	if err := q.Enqueue(bg, 3); err != nil {
		t.Fatalf("Enqueue after cancelled waiter: %v", err)
	}
	if v, err := q.Dequeue(bg); err != nil || v != 3 {
		t.Fatalf("Dequeue = %v, %v; want 3, nil", v, err)
	}
}

// TestCommitPointRace hammers the Enqueue/Close race: whenever Enqueue
// reports success the item must be drainable; whenever Close wins, Enqueue
// must report ErrClosed and leave nothing behind.
func TestCommitPointRace(t *testing.T) {
	for i := 0; i < 2000; i++ {
		q := queue.New[int](1)
		var wg sync.WaitGroup
		wg.Add(2)
		var enqErr error
		go func() { defer wg.Done(); enqErr = q.Enqueue(context.Background(), i) }()
		go func() { defer wg.Done(); q.Close() }()
		wg.Wait()
		v, err := q.Dequeue(context.Background())
		if enqErr == nil {
			if err != nil || v != i {
				t.Fatalf("iter %d: committed Enqueue but Dequeue = %v, %v", i, v, err)
			}
			if _, err := q.Dequeue(context.Background()); !errors.Is(err, queue.ErrClosed) {
				t.Fatalf("iter %d: second Dequeue = %v, want ErrClosed", i, err)
			}
		} else {
			if !errors.Is(enqErr, queue.ErrClosed) {
				t.Fatalf("iter %d: Enqueue = %v, want nil or ErrClosed", i, enqErr)
			}
			if !errors.Is(err, queue.ErrClosed) {
				t.Fatalf("iter %d: rejected Enqueue left item behind: %v, %v", i, v, err)
			}
		}
	}
}

// TestConcurrentIntegrity runs a larger race-checked workload and verifies
// no item is duplicated or lost, without invoking the history checker.
func TestConcurrentIntegrity(t *testing.T) {
	const producers, perProducer, consumers = 4, 200, 3
	q := queue.New[int](8)
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				v := p*perProducer + i
				for {
					err := q.Enqueue(context.Background(), v)
					if errors.Is(err, queue.ErrClosed) {
						return
					}
					if err == nil {
						break
					}
				}
			}
		}(p)
	}
	var mu sync.Mutex
	got := make(map[int]int)
	var consumersWG sync.WaitGroup
	for c := 0; c < consumers; c++ {
		consumersWG.Add(1)
		go func() {
			defer consumersWG.Done()
			for {
				v, err := q.Dequeue(context.Background())
				if errors.Is(err, queue.ErrClosed) {
					return
				}
				if err != nil {
					continue
				}
				mu.Lock()
				got[v]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	q.Close()
	consumersWG.Wait()
	if len(got) != producers*perProducer {
		t.Fatalf("got %d distinct values, want %d", len(got), producers*perProducer)
	}
	for v, n := range got {
		if n != 1 {
			t.Fatalf("value %d dequeued %d times", v, n)
		}
	}
}

// TestStressLinearizable records a concurrent history and verifies it with
// the lin checker. History size is bounded by lin.MaxOps.
func TestStressLinearizable(t *testing.T) {
	const capacity = 3
	const producers, perProducer = 2, 6
	q := queue.New[int](capacity)
	var rec lin.Recorder
	var producersWG sync.WaitGroup
	for p := 0; p < producers; p++ {
		producersWG.Add(1)
		go func(p int) {
			defer producersWG.Done()
			for i := 0; i < perProducer; i++ {
				v := p*perProducer + i + 1
				rec.Do(lin.Enqueue, v, func() (int, lin.Status) {
					// Occasionally use a short timeout to exercise
					// cancellation; retry so the item is not dropped.
					ctx := context.Background()
					if i%3 == 1 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
						defer cancel()
					}
					err := q.Enqueue(ctx, v)
					return 0, statusOf(err)
				})
			}
		}(p)
	}
	var consumersWG sync.WaitGroup
	for c := 0; c < 2; c++ {
		consumersWG.Add(1)
		go func() {
			defer consumersWG.Done()
			for {
				var closed bool
				rec.Do(lin.Dequeue, 0, func() (int, lin.Status) {
					v, err := q.Dequeue(context.Background())
					st := statusOf(err)
					closed = st == lin.Closed
					return v, st
				})
				if closed {
					return
				}
			}
		}()
	}
	producersWG.Wait()
	rec.Do(lin.Close, 0, func() (int, lin.Status) {
		q.Close()
		return 0, lin.OK
	})
	consumersWG.Wait()

	ops := rec.Ops()
	if len(ops) > lin.MaxOps {
		t.Fatalf("history too large: %d ops", len(ops))
	}
	order, ok, err := lin.Check(capacity, ops)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok {
		t.Fatalf("recorded history is not linearizable (%d ops)", len(ops))
	}
	if len(order) != len(ops) {
		t.Fatalf("witness covers %d of %d ops", len(order), len(ops))
	}
}
