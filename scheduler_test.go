package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

const waitTimeout = 3 * time.Second
const blockedGrace = 50 * time.Millisecond

type acquireResult struct {
	slot *Slot
	err  error
}

func acquireAsync(s *Scheduler, ctx context.Context) <-chan acquireResult {
	ch := make(chan acquireResult, 1)
	go func() {
		slot, err := s.Acquire(ctx)
		ch <- acquireResult{slot: slot, err: err}
	}()
	return ch
}

func mustAcquire(t *testing.T, s *Scheduler) *Slot {
	t.Helper()
	slot, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return slot
}

func waitResult(t *testing.T, ch <-chan acquireResult, what string) acquireResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(waitTimeout):
		t.Fatalf("timeout waiting for %s", what)
		return acquireResult{}
	}
}

func assertBlocked(t *testing.T, ch <-chan acquireResult, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s should be blocked, got slot=%v err=%v", what, r.slot != nil, r.err)
	case <-time.After(blockedGrace):
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// Lowering the limit must not interrupt running tasks, and must block new
// grants until running drops strictly below the new limit.
func TestDecreaseDoesNotStopRunning(t *testing.T) {
	s := New(3)
	a := mustAcquire(t, s)
	b := mustAcquire(t, s)
	c := mustAcquire(t, s)

	s.SetLimit(1)

	// All three tasks keep running; a fourth may not start.
	extra := acquireAsync(s, context.Background())
	assertBlocked(t, extra, "extra acquire while running(3) > limit(1)")

	a.Release()
	assertBlocked(t, extra, "extra acquire while running(2) > limit(1)")
	b.Release()
	assertBlocked(t, extra, "extra acquire while running(1) == limit(1)")

	c.Release()
	r := waitResult(t, extra, "extra acquire after running dropped below limit")
	if r.err != nil {
		t.Fatalf("extra acquire: %v", r.err)
	}
	r.slot.Release()
	if got := s.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0", got)
	}
}

// With limit zero, cancellation and Close must still work.
func TestZeroLimitAllowsCancelAndClose(t *testing.T) {
	s := New(1)
	held := mustAcquire(t, s)
	s.SetLimit(0)

	// Canceled waiter returns promptly even though no slot can ever be granted.
	ctx, cancel := context.WithCancel(context.Background())
	waiter := acquireAsync(s, ctx)
	waitFor(t, "waiter to enqueue", func() bool { return s.Queued() == 1 })
	cancel()
	if r := waitResult(t, waiter, "canceled waiter"); r.err != context.Canceled {
		t.Fatalf("canceled waiter err = %v, want context.Canceled", r.err)
	}

	// A non-canceled waiter is unblocked by Close, still at limit zero.
	stuck := acquireAsync(s, context.Background())
	waitFor(t, "second waiter to enqueue", func() bool { return s.Queued() == 1 })
	s.Close()
	if r := waitResult(t, stuck, "waiter after Close"); r.err != ErrClosed {
		t.Fatalf("waiter after Close err = %v, want ErrClosed", r.err)
	}

	// Releasing a previously granted slot still works at limit zero.
	held.Release()
	if got := s.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0", got)
	}

	// Close is idempotent; new Acquire fails immediately.
	s.Close()
	if _, err := s.Acquire(context.Background()); err != ErrClosed {
		t.Fatalf("Acquire after Close err = %v, want ErrClosed", err)
	}
}

// A canceled waiter must not consume a slot: the next queued waiter gets it.
func TestCancelFreesPlaceholder(t *testing.T) {
	s := New(1)
	first := mustAcquire(t, s)

	ctxB, cancelB := context.WithCancel(context.Background())
	waiterB := acquireAsync(s, ctxB)
	waiterC := acquireAsync(s, context.Background())
	waitFor(t, "B and C to enqueue", func() bool { return s.Queued() == 2 })

	cancelB()
	if r := waitResult(t, waiterB, "canceled waiter B"); r.err != context.Canceled {
		t.Fatalf("waiter B err = %v, want context.Canceled", r.err)
	}
	waitFor(t, "queue to drop B", func() bool { return s.Queued() == 1 })

	first.Release()
	r := waitResult(t, waiterC, "waiter C after slot freed")
	if r.err != nil {
		t.Fatalf("waiter C: %v", r.err)
	}
	if got := s.Running(); got != 1 {
		t.Fatalf("Running() = %d, want 1 (slot went to C, not to canceled B)", got)
	}
	r.slot.Release()
}

// Cancellation racing with a grant must hand the slot back to the queue.
func TestCancelRacingGrantHandsSlotBack(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := New(1)
		held := mustAcquire(t, s)

		ctx, cancel := context.WithCancel(context.Background())
		waiter := acquireAsync(s, ctx)
		waitFor(t, "waiter to enqueue", func() bool { return s.Queued() == 1 })

		// Fire cancellation and the freeing release concurrently: the grant
		// and the cancel race. Either way the slot must end up usable.
		go cancel()
		held.Release()

		r := waitResult(t, waiter, "racing waiter")
		if r.err == nil {
			// Grant won the race: release it ourselves.
			r.slot.Release()
		}
		waitFor(t, "slot to be returned", func() bool { return s.Running() == 0 })
		if got := s.Queued(); got != 0 {
			t.Fatalf("iteration %d: Queued() = %d, want 0", i, got)
		}
	}
}

// After the limit is restored, queued waiters are granted slots in FIFO order.
func TestFairnessAfterLimitRestore(t *testing.T) {
	const waiters = 5
	s := New(0)

	var mu sync.Mutex
	var order []int
	results := make([]<-chan acquireResult, waiters)
	for i := 0; i < waiters; i++ {
		i := i
		ch := make(chan acquireResult, 1)
		results[i] = ch
		go func() {
			slot, err := s.Acquire(context.Background())
			if err == nil {
				mu.Lock()
				order = append(order, i)
				mu.Unlock()
			}
			ch <- acquireResult{slot: slot, err: err}
		}()
		// Enqueue strictly in order so the expected FIFO order is well defined.
		waitFor(t, "waiter to enqueue", func() bool { return s.Queued() == i+1 })
	}

	// Restore capacity one slot at a time. With a single free slot, each
	// grant is unambiguous: waiter i must be granted before waiter i+1.
	for i := 0; i < waiters; i++ {
		s.SetLimit(1)
		var r acquireResult
		select {
		case r = <-results[i]:
		case <-time.After(waitTimeout):
			t.Fatalf("timeout waiting for waiter %d to be granted", i)
		}
		if r.err != nil {
			t.Fatalf("waiter %d: %v", i, r.err)
		}
		if i+1 < waiters {
			// Hold the slot while confirming no later waiter jumps the
			// queue, then free capacity for exactly one more waiter.
			for j := i + 1; j < waiters; j++ {
				assertBlocked(t, results[j], "later waiter while earlier slot held")
			}
			r.slot.Release()
			s.SetLimit(0)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != waiters {
		t.Fatalf("granted %d waiters, want %d", len(order), waiters)
	}
	for i := 0; i < waiters; i++ {
		if order[i] != i {
			t.Fatalf("grant order = %v, want FIFO 0..%d", order, waiters-1)
		}
	}
}

// A task finishing (Release) racing with a limit decrease: both interleavings
// are valid, but the scheduler must never grant a slot while running >= limit,
// and must never lose track of a slot.
func TestReleaseRacesWithSetLimit(t *testing.T) {
	// Deterministic interleavings.
	t.Run("release then decrease grants waiter", func(t *testing.T) {
		s := New(1)
		held := mustAcquire(t, s)
		waiter := acquireAsync(s, context.Background())
		waitFor(t, "waiter to enqueue", func() bool { return s.Queued() == 1 })

		held.Release() // running 0 < limit 1: waiter is granted
		s.SetLimit(0)  // decrease must not revoke the grant

		r := waitResult(t, waiter, "waiter granted before decrease")
		if r.err != nil {
			t.Fatalf("waiter: %v", r.err)
		}
		if got := s.Running(); got != 1 {
			t.Fatalf("Running() = %d, want 1 (grant survives decrease)", got)
		}
		r.slot.Release()
	})

	t.Run("decrease then release keeps waiter queued", func(t *testing.T) {
		s := New(1)
		held := mustAcquire(t, s)
		waiter := acquireAsync(s, context.Background())
		waitFor(t, "waiter to enqueue", func() bool { return s.Queued() == 1 })

		s.SetLimit(0)
		held.Release() // running 0, but limit 0: no grant

		assertBlocked(t, waiter, "waiter at limit 0 after release")
		if got := s.Running(); got != 0 {
			t.Fatalf("Running() = %d, want 0", got)
		}
		if got := s.Queued(); got != 1 {
			t.Fatalf("Queued() = %d, want 1", got)
		}

		s.SetLimit(1) // restoring the limit wakes the queued waiter
		r := waitResult(t, waiter, "waiter after limit restored")
		if r.err != nil {
			t.Fatalf("waiter: %v", r.err)
		}
		r.slot.Release()
	})

	// Concurrent churn: hammer Release and SetLimit together and verify the
	// final accounting is exact (run with -race).
	t.Run("concurrent churn", func(t *testing.T) {
		for i := 0; i < 200; i++ {
			s := New(1)
			held := mustAcquire(t, s)
			waiter := acquireAsync(s, context.Background())
			waitFor(t, "waiter to enqueue", func() bool { return s.Queued() == 1 })

			done := make(chan struct{}, 2)
			go func() { held.Release(); done <- struct{}{} }()
			go func() { s.SetLimit(0); done <- struct{}{} }()
			<-done
			<-done

			// Exactly one of these states is legal:
			//  - waiter granted before the decrease: running == 1
			//  - waiter still queued at limit 0:   running == 0, queued == 1
			switch {
			case s.Running() == 1 && s.Queued() == 0:
				r := waitResult(t, waiter, "waiter granted during churn")
				if r.err != nil {
					t.Fatalf("waiter: %v", r.err)
				}
				r.slot.Release()
			case s.Running() == 0 && s.Queued() == 1:
				assertBlocked(t, waiter, "waiter at limit 0 during churn")
				s.SetLimit(1)
				r := waitResult(t, waiter, "waiter after limit restored")
				if r.err != nil {
					t.Fatalf("waiter: %v", r.err)
				}
				r.slot.Release()
			default:
				t.Fatalf("iteration %d: illegal state running=%d queued=%d",
					i, s.Running(), s.Queued())
			}
			if got := s.Running(); got != 0 {
				t.Fatalf("iteration %d: Running() = %d, want 0", i, got)
			}
		}
	})
}

// Stress test: many workers, a toggling limit, and a live invariant check.
// The number of running tasks may exceed the limit only when the limit was
// lowered underneath them; it must never exceed the maximum limit ever set.
func TestStress(t *testing.T) {
	const workers = 16
	const maxLimit = 4

	s := New(maxLimit)

	var peakMu sync.Mutex
	peak := 0
	stop := make(chan struct{})
	var watcherWG sync.WaitGroup
	watcherWG.Add(1)
	go func() {
		defer watcherWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if r := s.Running(); r > maxLimit {
				peakMu.Lock()
				if r > peak {
					peak = r
				}
				peakMu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
				err := s.Run(ctx, func() {
					time.Sleep(time.Millisecond)
				})
				cancel()
				if err != nil {
					t.Errorf("Run: %v", err)
					return
				}
			}
		}()
	}

	// Toggle the limit, including down to zero, while work is in flight.
	for i := 0; i < 100; i++ {
		s.SetLimit(i % (maxLimit + 1))
		time.Sleep(time.Millisecond)
	}
	s.SetLimit(maxLimit)

	wg.Wait()
	close(stop)
	watcherWG.Wait()

	peakMu.Lock()
	if peak > 0 {
		t.Fatalf("running count exceeded maximum limit: %d > %d", peak, maxLimit)
	}
	peakMu.Unlock()
	if got := s.Running(); got != 0 {
		t.Fatalf("Running() = %d, want 0 after all workers finished", got)
	}
	if got := s.Queued(); got != 0 {
		t.Fatalf("Queued() = %d, want 0 after all workers finished", got)
	}
}
