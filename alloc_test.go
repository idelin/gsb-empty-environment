package mra

import (
	"context"
	"testing"
	"time"
)

func acquireAsync(a *Allocator, ctx context.Context, d Demands) chan error {
	ch := make(chan error, 1)
	go func() { ch <- a.Acquire(ctx, d) }()
	return ch
}

func mustAcquire(t *testing.T, a *Allocator, d Demands) {
	t.Helper()
	if err := a.Acquire(context.Background(), d); err != nil {
		t.Fatalf("Acquire(%v) failed: %v", d, err)
	}
}

// assertBlocked fails if ch delivers within the grace period.
func assertBlocked(t *testing.T, ch chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("%s: expected to stay blocked, got %v", what, err)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertGranted(t *testing.T, ch chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("%s: expected grant, got %v", what, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: expected grant, timed out", what)
	}
}

// All-or-nothing: a blocked request must not hold any resource partially.
func TestAllOrNothing(t *testing.T) {
	a := New(map[string]int64{"cpu": 2, "mem": 2})
	mustAcquire(t, a, Demands{"cpu": 2}) // cpu exhausted, mem free

	blocked := acquireAsync(a, context.Background(), Demands{"cpu": 1, "mem": 1})
	assertBlocked(t, blocked, "cpu exhausted")

	// While blocked, the requester must hold nothing: mem is still free,
	// so a mem-only request is granted (overtaking the blocked head).
	mustAcquire(t, a, Demands{"mem": 1})
	if got := a.Usage("mem"); got != 1 {
		t.Fatalf("mem usage = %d, want 1 (no partial hold by blocked request)", got)
	}

	a.Release(Demands{"cpu": 2})
	assertGranted(t, blocked, "after cpu release")
	a.Release(Demands{"cpu": 1, "mem": 1})
	a.Release(Demands{"mem": 1})
}

// Cancellation: a cancelled waiter is dequeued and holds nothing.
func TestCancel(t *testing.T) {
	a := New(map[string]int64{"cpu": 1})
	mustAcquire(t, a, Demands{"cpu": 1})

	ctx, cancel := context.WithCancel(context.Background())
	first := acquireAsync(a, ctx, Demands{"cpu": 1})
	second := acquireAsync(a, context.Background(), Demands{"cpu": 1})
	assertBlocked(t, first, "first waiter")
	assertBlocked(t, second, "second waiter")

	cancel()
	select {
	case err := <-first:
		if err != context.Canceled {
			t.Fatalf("cancelled Acquire returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled Acquire did not return")
	}
	if n := a.QueueLen(); n != 1 {
		t.Fatalf("queue len = %d, want 1 after cancel", n)
	}

	// The freed slot must go to the surviving waiter, not the cancelled one.
	a.Release(Demands{"cpu": 1})
	assertGranted(t, second, "second waiter after release")
	if got := a.Usage("cpu"); got != 1 {
		t.Fatalf("cpu usage = %d, want 1 (no leak from cancelled waiter)", got)
	}
	a.Release(Demands{"cpu": 1})
}

// Queue order with bounded overtaking: satisfiable requests skip the
// unsatisfiable head, but only MaxSkips times; then the head blocks all.
func TestSkipLimitHeadOfLineBlocking(t *testing.T) {
	a := New(map[string]int64{"cpu": 4}) // default MaxSkips = 3
	mustAcquire(t, a, Demands{"cpu": 4})

	head := acquireAsync(a, context.Background(), Demands{"cpu": 4})
	assertBlocked(t, head, "head needs full quota")

	var skipped []chan error
	for i := 0; i < 3; i++ {
		ch := acquireAsync(a, context.Background(), Demands{"cpu": 1})
		skipped = append(skipped, ch)
		assertBlocked(t, ch, "skip candidate queued")
	}

	// Three releases of 1 cpu each: each grant overtakes the head.
	for i := 0; i < 3; i++ {
		a.Release(Demands{"cpu": 1})
		assertGranted(t, skipped[i], "overtaking grant")
	}

	// Head has now been skipped 3 times: it blocks further overtaking.
	late := acquireAsync(a, context.Background(), Demands{"cpu": 1})
	assertBlocked(t, late, "late request queued")
	a.Release(Demands{"cpu": 1}) // 1 free cpu, but head blocks the queue
	assertBlocked(t, late, "head-of-line block after 3 skips")
	if got := a.Usage("cpu"); got != 3 {
		t.Fatalf("cpu usage = %d, want 3 (free cpu must not be granted past head)", got)
	}

	// Free the remaining cpu: head is granted, then the late request.
	a.Release(Demands{"cpu": 3})
	assertGranted(t, head, "head after enough capacity")
	a.Release(Demands{"cpu": 4}) // head releases its full allocation
	assertGranted(t, late, "late request after head")
	a.Release(Demands{"cpu": 1})
}

// Feasibility: demands above quota fail fast instead of blocking forever.
func TestExceedsQuotaFailsFast(t *testing.T) {
	a := New(map[string]int64{"cpu": 2})
	if err := a.Acquire(context.Background(), Demands{"cpu": 3}); err != ErrExceedsQuota {
		t.Fatalf("got %v, want ErrExceedsQuota", err)
	}
	if err := a.Acquire(context.Background(), Demands{"gpu": 1}); err != ErrExceedsQuota {
		t.Fatalf("unknown resource: got %v, want ErrExceedsQuota", err)
	}
	if err := a.Acquire(context.Background(), Demands{"cpu": 0}); err != ErrInvalidDemand {
		t.Fatalf("got %v, want ErrInvalidDemand", err)
	}
}

// Reentrancy: the release callback may call back into the allocator
// (Acquire/Release/SetQuota/Usage) without deadlocking.
func TestReleaseCallbackReentrancy(t *testing.T) {
	var a *Allocator
	done := make(chan struct{})
	depth := 0 // callbacks run synchronously on the releaser's goroutine

	a = New(map[string]int64{"cpu": 2}, WithOnRelease(func(d Demands) {
		depth++
		if depth > 1 {
			return // nested callback from the reentrant Release below
		}
		defer close(done)
		// Re-enter: synchronous acquire that fits, then release it.
		if err := a.Acquire(context.Background(), Demands{"cpu": 1}); err != nil {
			t.Errorf("reentrant Acquire: %v", err)
		}
		a.Release(Demands{"cpu": 1}) // nested Release -> nested callback
		a.SetQuota("cpu", 2)         // reentrant quota change
		_ = a.Usage("cpu")
	}))

	mustAcquire(t, a, Demands{"cpu": 2})
	a.Release(Demands{"cpu": 2}) // triggers callback which re-enters

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("release callback deadlocked on reentrancy")
	}
	if got := a.Usage("cpu"); got != 0 {
		t.Fatalf("cpu usage = %d, want 0 after reentrant release", got)
	}
}

// Callback observes the state after the grant loop ran.
func TestReleaseCallbackRunsAfterGrants(t *testing.T) {
	var a *Allocator
	usageInCallback := make(chan int64, 1)
	a = New(map[string]int64{"cpu": 1}, WithOnRelease(func(d Demands) {
		usageInCallback <- a.Usage("cpu")
	}))

	mustAcquire(t, a, Demands{"cpu": 1})
	waiting := acquireAsync(a, context.Background(), Demands{"cpu": 1})
	assertBlocked(t, waiting, "waiter queued")

	a.Release(Demands{"cpu": 1})
	assertGranted(t, waiting, "waiter granted before callback")
	select {
	case u := <-usageInCallback:
		if u != 1 {
			t.Fatalf("usage seen in callback = %d, want 1 (grant happened first)", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback not invoked")
	}
	a.Release(Demands{"cpu": 1})
	<-usageInCallback
}

// Quota shrink below current usage: holders keep resources, new grants
// wait until usage drops below the new quota.
func TestQuotaShrink(t *testing.T) {
	a := New(map[string]int64{"cpu": 4})
	mustAcquire(t, a, Demands{"cpu": 4})

	a.SetQuota("cpu", 2) // shrink below usage (4 > 2)

	blocked := acquireAsync(a, context.Background(), Demands{"cpu": 1})
	assertBlocked(t, blocked, "usage above shrunk quota")

	a.Release(Demands{"cpu": 2}) // usage 2, still 2+1 > 2
	assertBlocked(t, blocked, "usage equal to quota still blocks")

	a.Release(Demands{"cpu": 1}) // usage 1, now 1+1 <= 2
	assertGranted(t, blocked, "usage below shrunk quota")
	if got := a.Usage("cpu"); got != 2 {
		t.Fatalf("cpu usage = %d, want 2", got)
	}

	// Shrinking below current usage blocks new grants even when the
	// demand itself fits within the new quota.
	a.SetQuota("cpu", 1) // usage is 2, above the new quota
	starved := acquireAsync(a, context.Background(), Demands{"cpu": 1})
	assertBlocked(t, starved, "usage above shrunk quota")
	a.Release(Demands{"cpu": 2}) // usage 0 -> grant loop runs on release
	assertGranted(t, starved, "usage dropped below quota")
	a.Release(Demands{"cpu": 1})

	// Zero quota makes any positive demand infeasible: fail fast.
	a.SetQuota("cpu", 0)
	if err := a.Acquire(context.Background(), Demands{"cpu": 1}); err != ErrExceedsQuota {
		t.Fatalf("zero quota: got %v, want ErrExceedsQuota", err)
	}
}

// Cancel racing a grant: whichever wins, no resource leaks.
func TestCancelGrantRace(t *testing.T) {
	for i := 0; i < 200; i++ {
		a := New(map[string]int64{"cpu": 1})
		mustAcquire(t, a, Demands{"cpu": 1})
		ctx, cancel := context.WithCancel(context.Background())
		ch := acquireAsync(a, ctx, Demands{"cpu": 1})
		go cancel()
		a.Release(Demands{"cpu": 1})
		err := <-ch
		if err == nil {
			a.Release(Demands{"cpu": 1}) // grant won the race: give it back
		}
		if got := a.Usage("cpu"); got != 0 {
			t.Fatalf("iter %d: cpu usage = %d, want 0 (leak)", i, got)
		}
	}
}
