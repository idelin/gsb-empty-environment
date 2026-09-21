package tasksched

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func waitForPending(t *testing.T, clk *ManualClock, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for clk.Pending() != n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d pending timers, have %d", n, clk.Pending())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSuccessFirstAttempt(t *testing.T) {
	clk := NewManualClock(time.Unix(1000, 0))
	calls := 0
	out, err := Run(context.Background(), clk, "task-1", 3,
		[]time.Duration{time.Second},
		func(ctx context.Context, attempt int) error {
			calls++
			return nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if len(out.Attempts) != 1 || out.Attempts[0].Number != 1 || out.Attempts[0].Err != nil {
		t.Fatalf("bad history: %+v", out.Attempts)
	}
	if out.TaskID != "task-1" {
		t.Fatalf("bad task ID: %q", out.TaskID)
	}
}

func TestRetriesFollowBackoffSequence(t *testing.T) {
	start := time.Unix(1000, 0)
	clk := NewManualClock(start)
	backoff := []time.Duration{10 * time.Second, 30 * time.Second}

	done := make(chan struct{})
	var out Outcome
	var runErr error
	go func() {
		defer close(done)
		out, runErr = Run(context.Background(), clk, "task-2", 3, backoff,
			func(ctx context.Context, attempt int) error {
				if attempt < 3 {
					return errBoom
				}
				return nil
			})
	}()

	// Attempt 1 fails at t=1000; timer for 10s is registered.
	waitForPending(t, clk, 1)
	clk.Advance(9 * time.Second)
	select {
	case <-done:
		t.Fatal("run finished before backoff elapsed")
	default:
	}
	clk.Advance(1 * time.Second) // fires retry 2 at t=1010

	// Attempt 2 fails at t=1010; timer for 30s is registered.
	waitForPending(t, clk, 1)
	clk.Advance(30 * time.Second) // fires retry 3 at t=1040

	<-done
	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if len(out.Attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(out.Attempts))
	}
	wantStarts := []time.Time{start, start.Add(10 * time.Second), start.Add(40 * time.Second)}
	for i, a := range out.Attempts {
		if a.Number != i+1 {
			t.Fatalf("attempt %d has number %d", i, a.Number)
		}
		if !a.StartedAt.Equal(wantStarts[i]) {
			t.Fatalf("attempt %d started at %v, want %v", a.Number, a.StartedAt, wantStarts[i])
		}
	}
	if out.Attempts[0].Err != errBoom || out.Attempts[1].Err != errBoom || out.Attempts[2].Err != nil {
		t.Fatalf("bad attempt errors: %+v", out.Attempts)
	}
}

func TestMaxAttemptsReturnsLastErrorAndHistory(t *testing.T) {
	clk := NewManualClock(time.Unix(1000, 0))
	errs := []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}

	done := make(chan struct{})
	var out Outcome
	var runErr error
	go func() {
		defer close(done)
		out, runErr = Run(context.Background(), clk, "task-3", 3,
			[]time.Duration{time.Second},
			func(ctx context.Context, attempt int) error { return errs[attempt-1] })
	}()
	for i := 0; i < 2; i++ {
		waitForPending(t, clk, 1)
		clk.Advance(time.Second)
	}
	<-done

	if runErr != errs[2] {
		t.Fatalf("expected last error %v, got %v", errs[2], runErr)
	}
	if len(out.Attempts) != 3 {
		t.Fatalf("expected full history of 3 attempts, got %d", len(out.Attempts))
	}
	for i, a := range out.Attempts {
		if a.Err != errs[i] {
			t.Fatalf("attempt %d error = %v, want %v", a.Number, a.Err, errs[i])
		}
	}
	if clk.Pending() != 0 {
		t.Fatalf("expected no pending timers after completion, have %d", clk.Pending())
	}
}

func TestCancelDuringBackoffDoesNotWait(t *testing.T) {
	clk := NewManualClock(time.Unix(1000, 0))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, clk, "task-4", 5,
			[]time.Duration{time.Hour}, // huge backoff: must not be waited out
			func(ctx context.Context, attempt int) error { return errBoom })
		done <- err
	}()

	waitForPending(t, clk, 1)
	cancel() // cancel while sleeping in backoff; clock never advances

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel during backoff blocked on the timer")
	}
	if clk.Pending() != 0 {
		t.Fatalf("cancelled timer still pending")
	}
}

func TestLateTimerNeverRestartsCompletedTask(t *testing.T) {
	clk := NewManualClock(time.Unix(1000, 0))
	var calls atomic.Int32

	out, err := Run(context.Background(), clk, "task-5", 3,
		[]time.Duration{time.Minute},
		func(ctx context.Context, attempt int) error {
			calls.Add(1)
			return nil // succeeds immediately
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Long after completion, any stray timer would fire here.
	clk.Advance(time.Hour)
	clk.Advance(time.Hour)
	if got := calls.Load(); got != 1 {
		t.Fatalf("task ran %d times after completion", got)
	}
	if len(out.Attempts) != 1 {
		t.Fatalf("history grew after completion: %+v", out.Attempts)
	}
}

func TestCancelExpiryRaceHasSingleResult(t *testing.T) {
	// Hammer the cancel-vs-expiry race; the run must resolve exactly once
	// and never both run the next attempt and report cancellation.
	for i := 0; i < 200; i++ {
		clk := NewManualClock(time.Unix(1000, 0))
		ctx, cancel := context.WithCancel(context.Background())
		var calls atomic.Int32

		done := make(chan error, 1)
		go func() {
			_, err := Run(ctx, clk, "task-6", 2,
				[]time.Duration{time.Second},
				func(ctx context.Context, attempt int) error {
					calls.Add(1)
					return errBoom
				})
			done <- err
		}()

		waitForPending(t, clk, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); clk.Advance(time.Second) }()
		wg.Wait()

		select {
		case err := <-done:
			if errors.Is(err, context.Canceled) {
				if n := calls.Load(); n != 1 {
					t.Fatalf("iter %d: cancelled but task ran %d times", i, n)
				}
			} else if err != errBoom {
				t.Fatalf("iter %d: unexpected error %v", i, err)
			} else if n := calls.Load(); n != 2 {
				t.Fatalf("iter %d: expired path ran task %d times", i, n)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: run did not resolve", i)
		}
	}
}

func TestBackoffSequenceShorterThanAttempts(t *testing.T) {
	// Last backoff entry repeats when attempts outnumber the sequence.
	clk := NewManualClock(time.Unix(1000, 0))
	backoff := []time.Duration{5 * time.Second}

	done := make(chan struct{})
	var out Outcome
	go func() {
		defer close(done)
		out, _ = Run(context.Background(), clk, "task-7", 3, backoff,
			func(ctx context.Context, attempt int) error { return errBoom })
	}()
	for i := 0; i < 2; i++ {
		waitForPending(t, clk, 1)
		clk.Advance(5 * time.Second)
	}
	<-done
	want := []time.Time{
		time.Unix(1000, 0),
		time.Unix(1005, 0),
		time.Unix(1010, 0),
	}
	for i, a := range out.Attempts {
		if !a.StartedAt.Equal(want[i]) {
			t.Fatalf("attempt %d at %v, want %v", a.Number, a.StartedAt, want[i])
		}
	}
}
