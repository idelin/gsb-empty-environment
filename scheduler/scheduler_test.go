package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

// Reducing the limit must not interrupt running jobs, and must not admit
// queued jobs while running >= limit.
func TestReduceLimitDoesNotStopRunning(t *testing.T) {
	s := New(2)
	defer s.Close()

	release := make(chan struct{})
	started := make(chan string, 4)
	job := func(name string) func(context.Context) {
		return func(context.Context) {
			started <- name
			<-release
		}
	}

	if _, err := s.Submit(context.Background(), job("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), job("b")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Running() == 2 }, "two jobs running")

	s.SetLimit(1)
	if got := s.Running(); got != 2 {
		t.Fatalf("running jobs were interrupted: running=%d want 2", got)
	}

	// Queue a third job; it must not start while running (2) >= limit (1).
	if _, err := s.Submit(context.Background(), job("c")); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-started:
		if name == "c" {
			t.Fatal("queued job started above the reduced limit")
		}
	case <-time.After(50 * time.Millisecond):
	}

	// Drain one: running drops to 1, still not below limit, "c" stays queued.
	release <- struct{}{}
	waitFor(t, func() bool { return s.Running() == 1 }, "one job finished")
	select {
	case name := <-started:
		if name == "c" {
			t.Fatal("queued job started while running == limit")
		}
	case <-time.After(50 * time.Millisecond):
	}

	// Drain the second: now a slot is free and "c" is admitted.
	release <- struct{}{}
	waitFor(t, func() bool { return s.Running() == 1 }, "c admitted")
	if name := <-started; name != "c" {
		t.Fatalf("started %q, want %q", name, "c")
	}
	release <- struct{}{}
}

// A task finishing and a limit reduction racing each other must never lead
// to admission above the limit.
func TestFinishRacesWithLimitReduction(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := New(1)
		release := make(chan struct{})
		ran := make(chan string, 2)
		job := func(name string) func(context.Context) {
			return func(context.Context) { ran <- name; <-release }
		}
		first, err := s.Submit(context.Background(), job("first"))
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return s.Running() == 1 }, "first running")
		if name := <-ran; name != "first" {
			t.Fatalf("iter %d: started %q, want %q", i, name, "first")
		}
		second, err := s.Submit(context.Background(), job("second"))
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); release <- struct{}{} }() // finish "first"
		go func() { defer wg.Done(); s.SetLimit(0) }()         // concurrently cut limit
		wg.Wait()
		if err := first.Wait(); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}

		// Two legal outcomes, never more than one admission:
		//  a) the finish dispatched "second" before the cut (running=1);
		//  b) the cut landed first, "second" stays queued (running=0).
		if got := s.Running(); got == 1 {
			// Case (a): admitted while the limit was still 1. The cut must
			// not interrupt it; let it finish.
			if name := <-ran; name != "second" {
				t.Fatalf("iter %d: started %q, want %q", i, name, "second")
			}
			release <- struct{}{}
			if err := second.Wait(); err != nil {
				t.Fatalf("iter %d: %v", i, err)
			}
			if got := s.Running(); got != 0 {
				t.Fatalf("iter %d: running=%d, want 0", i, got)
			}
		} else if got == 0 {
			// Case (b): no admission at limit 0. Restoring the limit must
			// admit the queued job.
			select {
			case name := <-ran:
				t.Fatalf("iter %d: %q admitted at limit 0", i, name)
			default:
			}
			s.SetLimit(1)
			waitFor(t, func() bool { return s.Running() == 1 }, "second admitted")
			if name := <-ran; name != "second" {
				t.Fatalf("iter %d: started %q, want %q", i, name, "second")
			}
			release <- struct{}{}
			if err := second.Wait(); err != nil {
				t.Fatalf("iter %d: %v", i, err)
			}
		} else {
			t.Fatalf("iter %d: running=%d, over-limit admission", i, got)
		}
		s.Close()
	}
}

// Canceling a queued task frees its placeholder: it must never consume an
// admission slot, and the next queued task takes its place.
func TestCancelQueuedFreesPlaceholder(t *testing.T) {
	s := New(1)
	defer s.Close()

	release := make(chan struct{})
	started := make(chan string, 4)
	job := func(name string) func(context.Context) {
		return func(context.Context) { started <- name; <-release }
	}

	if _, err := s.Submit(context.Background(), job("running")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Running() == 1 }, "first running")
	if name := <-started; name != "running" {
		t.Fatalf("started %q, want %q", name, "running")
	}

	t2, err := s.Submit(context.Background(), job("canceled"))
	if err != nil {
		t.Fatal(err)
	}
	t3, err := s.Submit(context.Background(), job("next"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Queued() == 2 }, "two queued")

	if !t2.Cancel() {
		t.Fatal("Cancel on queued task returned false")
	}
	if got := s.Queued(); got != 1 {
		t.Fatalf("queued=%d want 1 after cancel", got)
	}
	if err := t2.Wait(); err != ErrCanceled {
		t.Fatalf("canceled task Wait() = %v, want ErrCanceled", err)
	}
	if !t2.Cancel() {
		// Canceling again must be a no-op, not a panic.
	} else {
		t.Fatal("second Cancel returned true")
	}

	// Free the slot: the canceled placeholder must be skipped, "next" runs.
	release <- struct{}{}
	waitFor(t, func() bool { return s.Running() == 1 }, "next admitted")
	if name := <-started; name != "next" {
		t.Fatalf("started %q, want %q", name, "next")
	}
	release <- struct{}{}
	if err := t3.Wait(); err != nil {
		t.Fatal(err)
	}
}

// Canceling via context while queued also frees the placeholder.
func TestContextCancelQueued(t *testing.T) {
	s := New(1)
	defer s.Close()

	release := make(chan struct{})
	if _, err := s.Submit(context.Background(), func(context.Context) { <-release }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Running() == 1 }, "first running")

	ctx, cancel := context.WithCancel(context.Background())
	queued, err := s.Submit(ctx, func(context.Context) {})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := queued.Wait(); err != context.Canceled {
		t.Fatalf("Wait() = %v, want context.Canceled", err)
	}
	waitFor(t, func() bool { return s.Queued() == 0 }, "placeholder removed")
	release <- struct{}{}
}

// With the limit at zero, Submit still queues, and Cancel/Close still work.
func TestZeroLimitAllowsCancelAndClose(t *testing.T) {
	s := New(0)

	task, err := s.Submit(context.Background(), func(context.Context) {
		t.Error("job must never run at limit 0")
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Queued() == 1 }, "queued at limit 0")

	if !task.Cancel() {
		t.Fatal("Cancel failed at limit 0")
	}

	other, err := s.Submit(context.Background(), func(context.Context) {
		t.Error("job must never run at limit 0")
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := other.Wait(); err != ErrClosed {
		t.Fatalf("Wait() = %v, want ErrClosed", err)
	}
	if _, err := s.Submit(context.Background(), func(context.Context) {}); err != ErrClosed {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
	s.Close() // idempotent
}

// After the limit is restored, the queued backlog drains in submission order.
func TestFairnessAfterLimitRestored(t *testing.T) {
	s := New(1)
	defer s.Close()

	release := make(chan struct{})
	if _, err := s.Submit(context.Background(), func(context.Context) { <-release }); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Running() == 1 }, "blocker running")

	// Freeze admissions, then enqueue a backlog.
	s.SetLimit(0)
	const n = 8
	var mu sync.Mutex
	order := make([]int, 0, n)
	tasks := make([]*Task, n)
	for i := 0; i < n; i++ {
		i := i
		task, err := s.Submit(context.Background(), func(context.Context) {
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
		})
		if err != nil {
			t.Fatal(err)
		}
		tasks[i] = task
	}
	waitFor(t, func() bool { return s.Queued() == n }, "backlog queued")

	// Restore a single slot and release the blocker: the backlog must drain
	// strictly in submission order, one at a time.
	s.SetLimit(1)
	release <- struct{}{}

	for i, task := range tasks {
		if err := task.Wait(); err != nil {
			t.Fatalf("task %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != n {
		t.Fatalf("ran %d jobs, want %d", len(order), n)
	}
	for i, got := range order {
		if got != i {
			t.Fatalf("admission order %v, want FIFO 0..%d", order, n-1)
		}
	}
}
