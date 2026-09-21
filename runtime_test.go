package tasklock

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBasicMutualExclusion(t *testing.T) {
	rt := NewRuntime(nil)
	lock := rt.NewLock("db")
	a := rt.NewTask("a", 1)
	b := rt.NewTask("b", 1)

	if err := a.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	go func() { acquired <- b.Acquire(lock, 0) }()

	select {
	case <-acquired:
		t.Fatal("b acquired lock while a holds it")
	case <-time.After(50 * time.Millisecond):
	}
	if err := a.Release(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	if got := lock.Owner(); got != b {
		t.Fatalf("owner = %v, want b", got)
	}
	if err := b.Release(lock); err != nil {
		t.Fatal(err)
	}
	if err := b.Release(lock); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("double release = %v, want ErrNotOwner", err)
	}
}

func TestPriorityInheritanceBoostAndRevert(t *testing.T) {
	rt := NewRuntime(nil)
	lock := rt.NewLock("db")
	low := rt.NewTask("low", 1)
	high := rt.NewTask("high", 10)

	if err := low.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
	done := rt.Go(high, func(tk *Task) error { return tk.Acquire(lock, 0) })

	waitFor(t, "low inherits high's priority", func() bool { return low.Priority() == 10 })
	if got := low.BasePriority(); got != 1 {
		t.Fatalf("base priority mutated: got %d, want 1", got)
	}

	if err := low.Release(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "boost reverts after release", func() bool { return low.Priority() == 1 })
	if err := high.Release(lock); err != nil {
		t.Fatal(err)
	}
}

// A waits on a lock held by B, B waits on a lock held by C: A's
// priority must reach C. After A is canceled, the boosts fall back.
func TestTransitiveInheritanceAndCancelFallback(t *testing.T) {
	rt := NewRuntime(nil)
	lockAB := rt.NewLock("lock-held-by-b") // A waits on B
	lockBC := rt.NewLock("lock-held-by-c") // B waits on C
	a := rt.NewTask("A", 9)
	b := rt.NewTask("B", 5)
	c := rt.NewTask("C", 1)

	if err := c.Acquire(lockBC, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(lockAB, 0); err != nil {
		t.Fatal(err)
	}
	bDone := rt.Go(b, func(tk *Task) error { return tk.Acquire(lockBC, 0) })
	waitFor(t, "C inherits B's priority", func() bool { return c.Priority() == 5 })

	aDone := rt.Go(a, func(tk *Task) error { return tk.Acquire(lockAB, 0) })
	waitFor(t, "A's priority reaches B", func() bool { return b.Priority() == 9 })
	waitFor(t, "A's priority reaches C transitively", func() bool { return c.Priority() == 9 })

	// Cancel A: its boost must fall back along the whole chain.
	rt.Cancel(a)
	if err := <-aDone; !errors.Is(err, ErrCanceled) {
		t.Fatalf("A acquire = %v, want ErrCanceled", err)
	}
	waitFor(t, "B falls back to its own wait", func() bool { return b.Priority() == 5 })
	waitFor(t, "C falls back to B's priority", func() bool { return c.Priority() == 5 })

	// Unwind: C releases, B acquires and releases, priorities revert.
	if err := c.Release(lockBC); err != nil {
		t.Fatal(err)
	}
	if err := <-bDone; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "C back to base", func() bool { return c.Priority() == 1 })
	if err := b.Release(lockBC); err != nil {
		t.Fatal(err)
	}
	if err := b.Release(lockAB); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "B back to base", func() bool { return b.Priority() == 5 })
}

// A waiter's timeout must revoke the boost it caused, with the timeout
// driven by an injected manual clock.
func TestTimeoutRevokesBoost(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	rt := NewRuntime(clock)
	lock := rt.NewLock("db")
	holder := rt.NewTask("holder", 1)
	waiter := rt.NewTask("waiter", 8)

	if err := holder.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
	done := rt.Go(waiter, func(tk *Task) error { return tk.Acquire(lock, 50*time.Millisecond) })
	waitFor(t, "holder boosted", func() bool { return holder.Priority() == 8 })

	clock.Advance(60 * time.Millisecond)
	if err := <-done; !errors.Is(err, ErrTimeout) {
		t.Fatalf("acquire = %v, want ErrTimeout", err)
	}
	waitFor(t, "boost revoked after timeout", func() bool { return holder.Priority() == 1 })

	// The lock is still held and usable.
	if err := holder.Release(lock); err != nil {
		t.Fatal(err)
	}
	if err := waiter.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
}

// Releasing one lock must not erase inheritance earned through another
// lock the task still holds.
func TestReleaseOneLockKeepsOtherLocksInheritance(t *testing.T) {
	rt := NewRuntime(nil)
	l1 := rt.NewLock("l1")
	l2 := rt.NewLock("l2")
	h := rt.NewTask("holder", 1)
	w1 := rt.NewTask("waiter-5", 5)
	w2 := rt.NewTask("waiter-9", 9)

	for _, l := range []*Lock{l1, l2} {
		if err := h.Acquire(l, 0); err != nil {
			t.Fatal(err)
		}
	}
	w1Done := rt.Go(w1, func(tk *Task) error { return tk.Acquire(l1, 0) })
	waitFor(t, "h inherits 5 via l1", func() bool { return h.Priority() == 5 })
	w2Done := rt.Go(w2, func(tk *Task) error { return tk.Acquire(l2, 0) })
	waitFor(t, "h inherits 9 via l2", func() bool { return h.Priority() == 9 })

	// Release l2: inheritance from l1's waiter must survive.
	if err := h.Release(l2); err != nil {
		t.Fatal(err)
	}
	if err := <-w2Done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "h keeps l1 inheritance", func() bool { return h.Priority() == 5 })

	if err := h.Release(l1); err != nil {
		t.Fatal(err)
	}
	if err := <-w1Done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "h back to base", func() bool { return h.Priority() == 1 })
}

// The highest-priority waiter is granted first.
func TestGrantOrderPrefersHighPriority(t *testing.T) {
	rt := NewRuntime(nil)
	lock := rt.NewLock("db")
	holder := rt.NewTask("holder", 1)
	lowW := rt.NewTask("low-waiter", 3)
	highW := rt.NewTask("high-waiter", 7)

	if err := holder.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
	lowDone := rt.Go(lowW, func(tk *Task) error { return tk.Acquire(lock, 0) })
	waitFor(t, "low waiter queued", func() bool { return lowW.BlockedOn() == lock })
	highDone := rt.Go(highW, func(tk *Task) error { return tk.Acquire(lock, 0) })
	waitFor(t, "high waiter queued", func() bool { return highW.BlockedOn() == lock })

	if err := holder.Release(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-highDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lowDone:
		t.Fatalf("low waiter granted before high waiter: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := highW.Release(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-lowDone; err != nil {
		t.Fatal(err)
	}
}

// A wait-for cycle must be reported as a diagnosable DeadlockError
// naming the tasks in the cycle.
func TestDeadlockCycleDiagnosable(t *testing.T) {
	rt := NewRuntime(nil)
	l1 := rt.NewLock("l1")
	l2 := rt.NewLock("l2")
	a := rt.NewTask("A", 1)
	b := rt.NewTask("B", 1)

	if err := a.Acquire(l1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(l2, 0); err != nil {
		t.Fatal(err)
	}
	aDone := rt.Go(a, func(tk *Task) error { return tk.Acquire(l2, 0) })
	waitFor(t, "A blocked on l2", func() bool { return a.BlockedOn() == l2 })

	err := b.Acquire(l1, 0)
	var dl *DeadlockError
	if !errors.As(err, &dl) {
		t.Fatalf("acquire = %v, want *DeadlockError", err)
	}
	if len(dl.Cycle) != 2 || dl.Cycle[0] != b || dl.Cycle[1] != a {
		t.Fatalf("cycle = %v, want [B A]", dl.Cycle)
	}
	want := "tasklock: deadlock cycle: B -> A -> B"
	if got := dl.Error(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}

	// Break the cycle and confirm both tasks can finish.
	rt.Cancel(a)
	if err := <-aDone; !errors.Is(err, ErrCanceled) {
		t.Fatal(err)
	}
	if err := a.Release(l1); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(l1, 0); err != nil {
		t.Fatal(err)
	}
}

func TestReentrantAcquireRejected(t *testing.T) {
	rt := NewRuntime(nil)
	lock := rt.NewLock("db")
	a := rt.NewTask("a", 1)
	if err := a.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Acquire(lock, 0); !errors.Is(err, ErrReentrant) {
		t.Fatalf("reentrant acquire = %v, want ErrReentrant", err)
	}
}

// Task functions and their failures are injected by the caller.
func TestInjectedTaskFunctionAndFailure(t *testing.T) {
	rt := NewRuntime(nil)
	worker := rt.NewTask("worker", 1)
	want := errors.New("injected boom")
	done := rt.Go(worker, func(tk *Task) error { return want })
	if got := <-done; !errors.Is(got, want) {
		t.Fatalf("got %v, want injected error %v", got, want)
	}

	// An injected function can also cancel another task mid-wait.
	lock := rt.NewLock("db")
	holder := rt.NewTask("holder", 1)
	waiter := rt.NewTask("waiter", 9)
	if err := holder.Acquire(lock, 0); err != nil {
		t.Fatal(err)
	}
	waitDone := rt.Go(waiter, func(tk *Task) error { return tk.Acquire(lock, 0) })
	waitFor(t, "waiter blocked", func() bool { return waiter.BlockedOn() == lock })
	killer := rt.NewTask("killer", 1)
	killDone := rt.Go(killer, func(tk *Task) error {
		rt.Cancel(waiter)
		return nil
	})
	if err := <-killDone; err != nil {
		t.Fatal(err)
	}
	if err := <-waitDone; !errors.Is(err, ErrCanceled) {
		t.Fatalf("waiter = %v, want ErrCanceled", err)
	}
	waitFor(t, "holder boost revoked", func() bool { return holder.Priority() == 1 })
}

// Stress the protocol under -race: many tasks contend over a few locks
// with nested acquisitions, timeouts and concurrent cancels.
func TestStressRace(t *testing.T) {
	rt := NewRuntime(nil)
	locks := []*Lock{rt.NewLock("l0"), rt.NewLock("l1"), rt.NewLock("l2")}
	tasks := make([]*Task, 16)
	for i := range tasks {
		tasks[i] = rt.NewTask(fmt.Sprintf("t%d", i), i%5)
	}

	var wg sync.WaitGroup
	var helpers sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(task *Task, seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for j := 0; j < 60; j++ {
				first := rng.Intn(len(locks))
				// Acquire up to two locks in a fixed global order so
				// the stress run itself cannot deadlock.
				second := first + 1
				if second >= len(locks) {
					second = first
				}
				timeout := time.Duration(1+rng.Intn(10)) * time.Millisecond
				if err := task.Acquire(locks[first], timeout); err != nil {
					continue
				}
				heldSecond := false
				if second != first && rng.Intn(2) == 0 {
					if err := task.Acquire(locks[second], timeout); err == nil {
						heldSecond = true
					}
				}
				time.Sleep(time.Duration(rng.Intn(2)) * time.Millisecond)
				if heldSecond {
					if err := task.Release(locks[second]); err != nil {
						t.Errorf("release second: %v", err)
					}
				}
				if err := task.Release(locks[first]); err != nil {
					t.Errorf("release first: %v", err)
				}
			}
		}(task, int64(i))
	}

	// Concurrent cancels and priority readers.
	stop := make(chan struct{})
	helpers.Add(2)
	go func() {
		defer helpers.Done()
		rng := rand.New(rand.NewSource(99))
		for {
			select {
			case <-stop:
				return
			default:
				rt.Cancel(tasks[rng.Intn(len(tasks))])
				time.Sleep(time.Millisecond)
			}
		}
	}()
	go func() {
		defer helpers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for _, tk := range tasks {
					_ = tk.Priority()
					_ = tk.BlockedOn()
				}
			}
		}
	}()

	wg.Wait() // workers finish; then stop the helpers
	close(stop)
	helpers.Wait()
}
