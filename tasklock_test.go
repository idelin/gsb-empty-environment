package tasklock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the test times out. It keeps
// goroutine orchestration deterministic without sleeping fixed amounts.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func mustLock(t *testing.T, s *Scheduler, task *Task, m *Mutex) {
	t.Helper()
	if err := s.Lock(context.Background(), task, m); err != nil {
		t.Fatalf("Lock(%s, %s): %v", task.Name(), m.Name(), err)
	}
}

func mustUnlock(t *testing.T, s *Scheduler, task *Task, m *Mutex) {
	t.Helper()
	if err := s.Unlock(task, m); err != nil {
		t.Fatalf("Unlock(%s, %s): %v", task.Name(), m.Name(), err)
	}
}

func TestBasicLockUnlock(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("m")
	a := s.NewTask("a", 1)

	mustLock(t, &s, a, m)
	if m.Owner() != a {
		t.Fatalf("owner = %v, want a", m.Owner())
	}
	mustUnlock(t, &s, a, m)
	if m.Owner() != nil {
		t.Fatalf("owner = %v, want nil", m.Owner())
	}
}

func TestUnlockByNonOwner(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("m")
	a := s.NewTask("a", 1)
	b := s.NewTask("b", 2)

	mustLock(t, &s, a, m)
	if err := s.Unlock(b, m); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("Unlock by non-owner = %v, want ErrNotOwner", err)
	}
}

// A low-priority holder is boosted while a high-priority task waits.
func TestPriorityInheritanceBoost(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("res")
	low := s.NewTask("low", 1)
	high := s.NewTask("high", 10)

	mustLock(t, &s, low, m)

	acquired := make(chan struct{})
	go func() {
		mustLock(t, &s, high, m)
		close(acquired)
	}()

	waitFor(t, "high to block on res", func() bool { return m.NumWaiters() == 1 })
	if got := low.EffectivePriority(); got != 10 {
		t.Fatalf("low effective priority = %d, want boosted 10", got)
	}

	mustUnlock(t, &s, low, m)
	<-acquired
	if got := low.EffectivePriority(); got != 1 {
		t.Fatalf("low effective priority after release = %d, want base 1", got)
	}
	if m.Owner() != high {
		t.Fatalf("owner = %v, want high", m.Owner())
	}
}

// A waits on B's lock and B waits on C's lock: C inherits A's priority.
func TestTransitiveInheritance(t *testing.T) {
	var s Scheduler
	m1 := s.NewMutex("m1") // held by b, wanted by a
	m2 := s.NewMutex("m2") // held by c, wanted by b
	a := s.NewTask("a", 10)
	b := s.NewTask("b", 5)
	c := s.NewTask("c", 1)

	mustLock(t, &s, b, m1)
	mustLock(t, &s, c, m2)

	bBlocked := make(chan struct{})
	go func() {
		defer close(bBlocked)
		mustLock(t, &s, b, m2) // b blocks on c
		mustUnlock(t, &s, b, m2)
	}()
	waitFor(t, "b to block on m2", func() bool { return m2.NumWaiters() == 1 })

	aBlocked := make(chan struct{})
	go func() {
		defer close(aBlocked)
		mustLock(t, &s, a, m1) // a blocks on b
		mustUnlock(t, &s, a, m1)
	}()
	waitFor(t, "a to block on m1", func() bool { return m1.NumWaiters() == 1 })

	if got := b.EffectivePriority(); got != 10 {
		t.Fatalf("b effective priority = %d, want 10 (from a)", got)
	}
	if got := c.EffectivePriority(); got != 10 {
		t.Fatalf("c effective priority = %d, want 10 (transitively from a)", got)
	}

	// Unwind: c releases m2 -> b gets it, releases -> b releases m1 -> a gets it.
	mustUnlock(t, &s, c, m2)
	<-bBlocked
	mustUnlock(t, &s, b, m1)
	<-aBlocked

	for task, want := range map[*Task]int{a: 10, b: 5, c: 1} {
		if got := task.EffectivePriority(); got != want {
			t.Fatalf("%s effective priority after unwind = %d, want base %d", task.Name(), got, want)
		}
	}
}

// Canceling the top waiter collapses the whole inheritance chain.
func TestCancelRevokesBoost(t *testing.T) {
	var s Scheduler
	m1 := s.NewMutex("m1")
	m2 := s.NewMutex("m2")
	a := s.NewTask("a", 10)
	b := s.NewTask("b", 5)
	c := s.NewTask("c", 1)

	mustLock(t, &s, b, m1)
	mustLock(t, &s, c, m2)

	go func() { mustLock(t, &s, b, m2) }()
	waitFor(t, "b to block on m2", func() bool { return m2.NumWaiters() == 1 })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Lock(ctx, a, m1) }()
	waitFor(t, "a to block on m1", func() bool { return m1.NumWaiters() == 1 })
	if got := c.EffectivePriority(); got != 10 {
		t.Fatalf("c effective priority = %d, want 10", got)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("a Lock returned %v, want context.Canceled", err)
	}
	if got := c.EffectivePriority(); got != 5 {
		t.Fatalf("c effective priority after cancel = %d, want 5 (only b's donation left)", got)
	}
	if got := b.EffectivePriority(); got != 5 {
		t.Fatalf("b effective priority after cancel = %d, want base 5", got)
	}
}

// A wait that times out returns ctx.Err() and revokes the donation.
func TestTimeoutRevokesBoost(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("res")
	low := s.NewTask("low", 1)
	high := s.NewTask("high", 10)

	mustLock(t, &s, low, m)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.Lock(ctx, high, m); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock returned %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) < 25*time.Millisecond {
		t.Fatalf("Lock returned before the injected timeout elapsed")
	}
	if got := low.EffectivePriority(); got != 1 {
		t.Fatalf("low effective priority after timeout = %d, want base 1", got)
	}
	if m.NumWaiters() != 0 {
		t.Fatalf("waiters after timeout = %d, want 0", m.NumWaiters())
	}
}

// Releasing one lock must not erase inheritance from another held lock.
func TestReleaseKeepsOtherInheritance(t *testing.T) {
	var s Scheduler
	m1 := s.NewMutex("m1")
	m2 := s.NewMutex("m2")
	h := s.NewTask("holder", 1)
	w1 := s.NewTask("w1", 7)
	w2 := s.NewTask("w2", 9)

	mustLock(t, &s, h, m1)
	mustLock(t, &s, h, m2)

	w1Done := make(chan struct{})
	go func() {
		defer close(w1Done)
		mustLock(t, &s, w1, m1)
		mustUnlock(t, &s, w1, m1)
	}()
	waitFor(t, "w1 to block on m1", func() bool { return m1.NumWaiters() == 1 })

	go func() { mustLock(t, &s, w2, m2) }()
	waitFor(t, "w2 to block on m2", func() bool { return m2.NumWaiters() == 1 })

	if got := h.EffectivePriority(); got != 9 {
		t.Fatalf("holder effective priority = %d, want 9", got)
	}

	// Release m1: w1's donation ends, but w2's boost of h must survive.
	mustUnlock(t, &s, h, m1)
	<-w1Done
	if got := h.EffectivePriority(); got != 9 {
		t.Fatalf("holder effective priority after releasing m1 = %d, want 9 (w2 still waiting)", got)
	}

	mustUnlock(t, &s, h, m2)
	waitFor(t, "w2 to acquire m2", func() bool { return m2.Owner() == w2 })
	if got := h.EffectivePriority(); got != 1 {
		t.Fatalf("holder effective priority after releasing both = %d, want base 1", got)
	}
}

// The highest-priority waiter is granted the lock first.
func TestHighestPriorityWaiterFirst(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("res")
	holder := s.NewTask("holder", 1)
	mid := s.NewTask("mid", 5)
	high := s.NewTask("high", 9)

	mustLock(t, &s, holder, m)

	midAcquired := make(chan struct{})
	go func() {
		mustLock(t, &s, mid, m)
		close(midAcquired)
	}()
	waitFor(t, "mid to block", func() bool { return m.NumWaiters() == 1 })

	highAcquired := make(chan struct{})
	go func() {
		mustLock(t, &s, high, m)
		close(highAcquired)
	}()
	waitFor(t, "high to block", func() bool { return m.NumWaiters() == 2 })

	mustUnlock(t, &s, holder, m)
	select {
	case <-highAcquired:
	case <-midAcquired:
		t.Fatal("mid acquired before higher-priority high")
	case <-time.After(5 * time.Second):
		t.Fatal("no waiter acquired after unlock")
	}
	if m.Owner() != high {
		t.Fatalf("owner = %v, want high", m.Owner())
	}
	mustUnlock(t, &s, high, m)
	<-midAcquired
}

// A wait-for cycle is rejected with a diagnosable DeadlockError.
func TestDeadlockDetection(t *testing.T) {
	var s Scheduler
	m1 := s.NewMutex("m1")
	m2 := s.NewMutex("m2")
	a := s.NewTask("a", 1)
	b := s.NewTask("b", 2)

	mustLock(t, &s, a, m1)
	mustLock(t, &s, b, m2)

	go func() { mustLock(t, &s, a, m2) }() // a waits on b
	waitFor(t, "a to block on m2", func() bool { return m2.NumWaiters() == 1 })

	err := s.Lock(context.Background(), b, m1) // b waiting on a closes the cycle
	var dl *DeadlockError
	if !errors.As(err, &dl) {
		t.Fatalf("Lock returned %v, want *DeadlockError", err)
	}
	if got, want := strings.Join(dl.Cycle, " -> "), "b -> a -> b"; got != want {
		t.Fatalf("cycle = %q, want %q", got, want)
	}
	t.Logf("diagnostic: %v", dl)

	// The rejected wait must not register: no waiter, no donation.
	if m1.NumWaiters() != 0 {
		t.Fatalf("m1 waiters = %d, want 0", m1.NumWaiters())
	}
	if got := a.EffectivePriority(); got != 1 {
		t.Fatalf("a effective priority = %d, want base 1", got)
	}
}

// Re-locking a mutex the task already holds is a self-deadlock.
func TestSelfDeadlock(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("m")
	a := s.NewTask("a", 1)

	mustLock(t, &s, a, m)
	var dl *DeadlockError
	if err := s.Lock(context.Background(), a, m); !errors.As(err, &dl) {
		t.Fatalf("re-lock returned %v, want *DeadlockError", err)
	} else if got, want := strings.Join(dl.Cycle, " -> "), "a -> a"; got != want {
		t.Fatalf("cycle = %q, want %q", got, want)
	}
}

// A grant racing with cancellation still results in ownership.
func TestGrantRacingCancel(t *testing.T) {
	var s Scheduler
	m := s.NewMutex("res")
	low := s.NewTask("low", 1)
	high := s.NewTask("high", 10)

	mustLock(t, &s, low, m)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Lock(ctx, high, m) }()
	waitFor(t, "high to block", func() bool { return m.NumWaiters() == 1 })

	mustUnlock(t, &s, low, m) // grant...
	cancel()                  // ...races with cancel
	if err := <-done; err != nil {
		t.Fatalf("Lock returned %v, want nil (grant won the race)", err)
	}
	if m.Owner() != high {
		t.Fatalf("owner = %v, want high", m.Owner())
	}
}
