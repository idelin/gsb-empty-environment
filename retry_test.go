package retrysched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// waitForTimers blocks until the manual clock has n pending timers, proving
// the scheduler has (re)armed a backoff timer without sleeping real time.
func waitForTimers(t *testing.T, c *ManualClock, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.Pending() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d pending timers, have %d", n, c.Pending())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSuccessFirstAttempt(t *testing.T) {
	s := NewScheduler(NewManualClock(time.Unix(0, 0)), []time.Duration{time.Second})
	h := s.Submit("job-1", func(context.Context, int) error { return nil })
	r := h.Wait()
	if r.Err != nil {
		t.Fatalf("want success, got %v", r.Err)
	}
	if r.ID != "job-1" || len(r.Attempts) != 1 || r.Attempts[0].Attempt != 1 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestBackoffSequenceSameIDDifferentAttempts(t *testing.T) {
	start := time.Unix(1000, 0)
	clock := NewManualClock(start)
	backoff := []time.Duration{10 * time.Second, 30 * time.Second}
	s := NewScheduler(clock, backoff)

	var mu sync.Mutex
	var calls []int
	h := s.Submit("job-42", func(_ context.Context, attempt int) error {
		mu.Lock()
		calls = append(calls, attempt)
		mu.Unlock()
		if attempt < 3 {
			return fmt.Errorf("fail %d", attempt)
		}
		return nil
	})

	waitForTimers(t, clock, 1) // attempt 1 failed, first backoff armed
	clock.Advance(10 * time.Second)
	waitForTimers(t, clock, 1) // attempt 2 failed, second backoff armed
	clock.Advance(30 * time.Second)

	r := h.Wait()
	if r.Err != nil {
		t.Fatalf("want success after retries, got %v", r.Err)
	}
	if r.ID != "job-42" {
		t.Fatalf("task ID changed across retries: %q", r.ID)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || calls[0] != 1 || calls[1] != 2 || calls[2] != 3 {
		t.Fatalf("attempts not 1,2,3: %v", calls)
	}
	if len(r.Attempts) != 3 {
		t.Fatalf("history missing attempts: %+v", r.Attempts)
	}
	// Explicit backoff sequence honored: attempts at t0, t0+10s, t0+40s.
	wantAt := []time.Time{start, start.Add(10 * time.Second), start.Add(40 * time.Second)}
	for i, w := range wantAt {
		if !r.Attempts[i].At.Equal(w) {
			t.Fatalf("attempt %d ran at %v, want %v", i+1, r.Attempts[i].At, w)
		}
	}
	if r.Attempts[0].Err == nil || r.Attempts[1].Err == nil || r.Attempts[2].Err != nil {
		t.Fatalf("history errors wrong: %+v", r.Attempts)
	}
}

func TestMaxAttemptsReturnsLastErrorAndHistory(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	s := NewScheduler(clock, []time.Duration{time.Second, 2 * time.Second})

	h := s.Submit("job-x", func(_ context.Context, attempt int) error {
		return fmt.Errorf("fail %d", attempt)
	})

	waitForTimers(t, clock, 1)
	clock.Advance(time.Second)
	waitForTimers(t, clock, 1)
	clock.Advance(2 * time.Second)

	r := h.Wait()
	if r.Err == nil || r.Err.Error() != "fail 3" {
		t.Fatalf("want last error %q, got %v", "fail 3", r.Err)
	}
	if len(r.Attempts) != 3 {
		t.Fatalf("want full history of 3 attempts, got %+v", r.Attempts)
	}
	for i, a := range r.Attempts {
		if a.Attempt != i+1 || a.Err == nil || a.Err.Error() != fmt.Sprintf("fail %d", i+1) {
			t.Fatalf("history entry %d wrong: %+v", i, a)
		}
	}
	if clock.Pending() != 0 {
		t.Fatalf("timer still pending after exhaustion")
	}
}

func TestCancelDuringBackoffDoesNotWaitRealTime(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	// Huge backoff: if cancel waited for the timer this test would hang.
	s := NewScheduler(clock, []time.Duration{time.Hour})

	h := s.Submit("job-c", func(context.Context, int) error { return errBoom })
	waitForTimers(t, clock, 1)

	begin := time.Now()
	if !h.Cancel() {
		t.Fatal("cancel during backoff not accepted")
	}
	r := h.Wait()
	elapsed := time.Since(begin)

	if !errors.Is(r.Err, ErrCanceled) {
		t.Fatalf("want ErrCanceled, got %v", r.Err)
	}
	if len(r.Attempts) != 1 {
		t.Fatalf("canceled task ran extra attempts: %+v", r.Attempts)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("cancel waited real time: %v", elapsed)
	}
	if clock.Pending() != 0 {
		t.Fatalf("backoff timer not stopped on cancel")
	}
}

func TestCancelAfterDoneRejected(t *testing.T) {
	s := NewScheduler(NewManualClock(time.Unix(0, 0)), []time.Duration{time.Second})
	h := s.Submit("job-d", func(context.Context, int) error { return nil })
	if r := h.Wait(); r.Err != nil {
		t.Fatalf("want success, got %v", r.Err)
	}
	if h.Cancel() {
		t.Fatal("cancel of finished task must be rejected")
	}
}

func TestCancelRunningAttemptPropagatesContext(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	s := NewScheduler(clock, []time.Duration{time.Second})

	started := make(chan struct{})
	h := s.Submit("job-r", func(ctx context.Context, _ int) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	if !h.Cancel() {
		t.Fatal("cancel of running attempt not accepted")
	}
	r := h.Wait()
	if !errors.Is(r.Err, ErrCanceled) {
		t.Fatalf("want ErrCanceled, got %v", r.Err)
	}
	if len(r.Attempts) != 1 {
		t.Fatalf("canceled running task retried: %+v", r.Attempts)
	}
}

// TestCancelExpiryRaceSingleResult hammers the cancel-vs-expiry race: a
// cancel and a timer firing concurrently must yield exactly one terminal
// result and never start an attempt after the task is done.
func TestCancelExpiryRaceSingleResult(t *testing.T) {
	for i := 0; i < 300; i++ {
		clock := NewManualClock(time.Unix(0, 0))
		s := NewScheduler(clock, []time.Duration{time.Second})

		var mu sync.Mutex
		attempts := 0
		h := s.Submit("job-race", func(ctx context.Context, attempt int) error {
			mu.Lock()
			attempts++
			mu.Unlock()
			if attempt == 1 {
				return errBoom
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		})
		waitForTimers(t, clock, 1)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); clock.Advance(time.Second) }()
		go func() { defer wg.Done(); h.Cancel() }()
		wg.Wait()

		r := h.Wait() // exactly one result, always
		mu.Lock()
		n := attempts
		mu.Unlock()
		switch {
		case errors.Is(r.Err, ErrCanceled):
			if n < 1 || n > 2 {
				t.Fatalf("iter %d: canceled with %d attempts", i, n)
			}
		case r.Err == nil:
			if n != 2 {
				t.Fatalf("iter %d: success with %d attempts", i, n)
			}
		default:
			t.Fatalf("iter %d: unexpected result %v", i, r.Err)
		}
		// No further activity may be scheduled after the terminal result.
		clock.Advance(time.Hour)
		mu.Lock()
		if attempts != n {
			t.Fatalf("iter %d: attempt started after completion", i)
		}
		mu.Unlock()
	}
}

// TestLateTimerDoesNotStartAfterCompletion stops a backoff timer via cancel,
// then replays its (stale) fire callback directly: it must be a no-op.
func TestLateTimerDoesNotStartAfterCompletion(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	s := NewScheduler(clock, []time.Duration{time.Second})

	attempts := 0
	var mu sync.Mutex
	h := s.Submit("job-late", func(context.Context, int) error {
		mu.Lock()
		attempts++
		mu.Unlock()
		return errBoom
	})
	waitForTimers(t, clock, 1)

	if !h.Cancel() {
		t.Fatal("cancel not accepted")
	}
	if r := h.Wait(); !errors.Is(r.Err, ErrCanceled) {
		t.Fatalf("want ErrCanceled, got %v", r.Err)
	}

	// Stale timer fires late (e.g. Stop lost a race on a real clock):
	// onTimer must refuse to start another attempt.
	s.onTimer(h.t)
	clock.Advance(time.Hour)

	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("late timer started attempt %d after completion", attempts)
	}
}

func TestManualClockFiresInOrder(t *testing.T) {
	clock := NewManualClock(time.Unix(0, 0))
	var order []string
	clock.AfterFunc(3*time.Second, func() { order = append(order, "c") })
	clock.AfterFunc(1*time.Second, func() { order = append(order, "a") })
	clock.AfterFunc(2*time.Second, func() {
		order = append(order, "b")
		// Timer scheduled from within a callback, due inside the window.
		clock.AfterFunc(500*time.Millisecond, func() { order = append(order, "d") })
	})
	clock.Advance(4 * time.Second)
	want := []string{"a", "b", "d", "c"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got %v, want %v", order, want)
		}
	}
	if clock.Pending() != 0 {
		t.Fatalf("timers left pending")
	}
}
