package budgetsem

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// barrier is a controllable one-shot gate used to orchestrate goroutines.
type barrier struct{ ch chan struct{} }

func newBarrier() *barrier { return &barrier{ch: make(chan struct{})} }
func (b *barrier) open()   { close(b.ch) }
func (b *barrier) wait()   { <-b.ch }

func (s *Semaphore) queueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue.Len()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// acquireAsync starts an Acquire and reports the outcome on the returned channel.
func acquireAsync(sem *Semaphore, ctx context.Context, n int64) <-chan error {
	done := make(chan error, 1)
	go func() { done <- sem.Acquire(ctx, n) }()
	return done
}

// assertBlocked fails if done delivers a result within the window.
func assertBlocked(t *testing.T, what string, done <-chan error) {
	select {
	case err := <-done:
		t.Fatalf("%s: expected blocked, got result %v", what, err)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestStrictFIFOHeadCannotBeOvertaken verifies the starvation rule: a queued
// oversized head request is never overtaken by a later smaller request that
// would otherwise fit.
func TestStrictFIFOHeadCannotBeOvertaken(t *testing.T) {
	sem := New(10)
	if err := sem.Acquire(context.Background(), 5); err != nil {
		t.Fatal(err)
	}

	big := acquireAsync(sem, context.Background(), 8) // head: 5+8 > 10, blocks
	waitFor(t, "big waiter queued", func() bool { return sem.queueLen() == 1 })

	small := acquireAsync(sem, context.Background(), 4) // 5+4 <= 10, would fit alone
	waitFor(t, "small waiter queued", func() bool { return sem.queueLen() == 2 })

	assertBlocked(t, "small waiter must not overtake the head", small)
	if got := sem.Used(); got != 5 {
		t.Fatalf("used = %d, want 5 (no hidden grant)", got)
	}

	sem.Release(5) // free 5: now 0+8 fits the head first
	waitFor(t, "big waiter granted", func() bool { return sem.Used() == 8 })
	if err := <-big; err != nil {
		t.Fatalf("big waiter: %v", err)
	}
	assertBlocked(t, "small waiter still queued behind big grant", small)

	sem.Release(8) // free 8: small finally fits
	if err := <-small; err != nil {
		t.Fatalf("small waiter: %v", err)
	}
	sem.Release(4)
	if got := sem.Used(); got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
}

// TestCancelHeadAdvancesQueueImmediately verifies that cancelling the head
// waiter advances the queue without any release of budget.
func TestCancelHeadAdvancesQueueImmediately(t *testing.T) {
	sem := New(10)
	if err := sem.Acquire(context.Background(), 6); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	head := acquireAsync(sem, ctx, 5) // head: 6+5 > 10, blocks
	waitFor(t, "head queued", func() bool { return sem.queueLen() == 1 })

	next := acquireAsync(sem, context.Background(), 4) // 6+4 <= 10, fits once head is gone
	waitFor(t, "next queued", func() bool { return sem.queueLen() == 2 })

	cancel() // no Release happens; the queue must advance on cancellation alone
	if err := <-head; err != context.Canceled {
		t.Fatalf("cancelled head: got %v, want context.Canceled", err)
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatalf("next waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue did not advance after head cancellation")
	}
	if got := sem.Used(); got != 10 {
		t.Fatalf("used = %d, want 10", got)
	}
	sem.Release(6)
	sem.Release(4)
}

// TestGrantCancelRaceExactlyOneOutcome hammers the grant/cancel race: when a
// release (which triggers a grant) and a cancellation fire simultaneously,
// exactly one outcome occurs and the budget accounting stays exact.
func TestGrantCancelRaceExactlyOneOutcome(t *testing.T) {
	const iterations = 500
	var grants, cancels int64

	for i := 0; i < iterations; i++ {
		sem := New(1)
		if err := sem.Acquire(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := acquireAsync(sem, ctx, 1)
		waitFor(t, "waiter queued", func() bool { return sem.queueLen() == 1 })

		start := newBarrier()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); start.wait(); sem.Release(1) }() // may grant the waiter
		go func() { defer wg.Done(); start.wait(); cancel() }()       // may cancel the waiter
		start.open()

		err := <-done
		wg.Wait()
		switch err {
		case nil:
			grants++
			sem.Release(1) // grant won: the caller owns and returns the byte
		case context.Canceled:
			cancels++
		default:
			t.Fatalf("iteration %d: unexpected error %v", i, err)
		}

		if got := sem.Used(); got != 0 {
			t.Fatalf("iteration %d: used = %d, want 0 (leaked or doubled budget)", i, got)
		}
		// A fresh acquire must succeed immediately: no phantom budget.
		if err := sem.Acquire(context.Background(), 1); err != nil {
			t.Fatalf("iteration %d: post-race acquire: %v", i, err)
		}
		sem.Release(1)
	}
	t.Logf("outcomes: %d grants, %d cancels (each exactly one result)", grants, cancels)
}

// TestShrinkDoesNotRevokeAndAbsorbsExcess verifies that shrinking capacity
// never revokes granted quota, and that released excess stock is absorbed
// rather than re-granted while usage stays above the new capacity.
func TestShrinkDoesNotRevokeAndAbsorbsExcess(t *testing.T) {
	sem := New(10)
	if err := sem.Acquire(context.Background(), 6); err != nil {
		t.Fatal(err)
	}
	if err := sem.Acquire(context.Background(), 4); err != nil {
		t.Fatal(err)
	}

	sem.SetCapacity(5)
	if got := sem.Used(); got != 10 {
		t.Fatalf("shrink revoked granted quota: used = %d, want 10", got)
	}

	waiter := acquireAsync(sem, context.Background(), 1)
	waitFor(t, "waiter queued", func() bool { return sem.queueLen() == 1 })

	sem.Release(4) // used 10 -> 6, still above capacity 5: excess absorbed
	assertBlocked(t, "excess stock must not be re-granted above capacity", waiter)
	if got := sem.Used(); got != 6 {
		t.Fatalf("used = %d, want 6", got)
	}

	sem.Release(6) // used 6 -> 0, now 0+1 <= 5: grant
	select {
	case err := <-waiter:
		if err != nil {
			t.Fatalf("waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not granted after usage dropped below capacity")
	}
	if got := sem.Used(); got != 1 {
		t.Fatalf("used = %d, want 1", got)
	}
	sem.Release(1)
}

// TestGrowGrantsQueuedWaitersInOrder verifies that growing capacity grants
// queued waiters in FIFO order and stops at the first that does not fit.
func TestGrowGrantsQueuedWaitersInOrder(t *testing.T) {
	sem := New(4)
	if err := sem.Acquire(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	w1 := acquireAsync(sem, context.Background(), 3)
	waitFor(t, "w1 queued", func() bool { return sem.queueLen() == 1 })
	w2 := acquireAsync(sem, context.Background(), 2)
	waitFor(t, "w2 queued", func() bool { return sem.queueLen() == 2 })

	sem.SetCapacity(8) // 4+3 <= 8 grants w1; 7+2 > 8 stops at w2
	if err := <-w1; err != nil {
		t.Fatalf("w1: %v", err)
	}
	assertBlocked(t, "w2 must wait for the head-of-line fit", w2)
	if got := sem.Used(); got != 7 {
		t.Fatalf("used = %d, want 7", got)
	}

	sem.Release(4) // 3+2 <= 8 grants w2
	if err := <-w2; err != nil {
		t.Fatalf("w2: %v", err)
	}
	sem.Release(3)
	sem.Release(2)
	if got := sem.Used(); got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
}

// TestBudgetConservation stresses concurrent acquire/release cycles behind a
// start barrier and verifies the budget invariant: usage never exceeds
// capacity, and every granted byte is eventually returned.
func TestBudgetConservation(t *testing.T) {
	const (
		capacity   = 64
		workers    = 16
		iterations = 500
	)
	sem := New(capacity)

	var maxUsed int64
	stop := make(chan struct{})
	var monitorWG sync.WaitGroup
	monitorWG.Add(1)
	go func() {
		defer monitorWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if u := sem.Used(); u > atomic.LoadInt64(&maxUsed) {
				atomic.StoreInt64(&maxUsed, u)
			}
		}
	}()

	start := newBarrier()
	var acquired, released int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start.wait()
			for i := 0; i < iterations; i++ {
				n := int64((id+i)%8 + 1)
				if err := sem.Acquire(context.Background(), n); err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				atomic.AddInt64(&acquired, n)
				sem.Release(n)
				atomic.AddInt64(&released, n)
			}
		}(w)
	}
	start.open()
	wg.Wait()
	close(stop)
	monitorWG.Wait()

	if maxUsed > capacity {
		t.Fatalf("budget violated: peak usage %d exceeded capacity %d", maxUsed, capacity)
	}
	if acquired != released {
		t.Fatalf("budget leak: acquired %d bytes, released %d", acquired, released)
	}
	if got := sem.Used(); got != 0 {
		t.Fatalf("used = %d, want 0 after drain", got)
	}
	t.Logf("conserved: %d bytes cycled, peak usage %d/%d", acquired, maxUsed, capacity)
}
