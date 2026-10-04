package budget

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// alwaysFailTransport simulates the layer that truly issues requests:
// it spends the token and fails every time.
func alwaysFailTransport(counter *int64) func(context.Context, *Token) error {
	return func(_ context.Context, tok *Token) error {
		if tok != nil && !tok.Spend() {
			return errors.New("token not spendable")
		}
		atomic.AddInt64(counter, 1)
		return errBoom
	}
}

func TestDoSuccessFirstAttempt(t *testing.T) {
	b := New(3)
	ctx, cancel := NewContext(context.Background(), b)
	defer cancel()
	calls := 0
	err := Do(ctx, Policy{MaxRetries: 3}, func(_ context.Context, tok *Token) error {
		calls++
		tok.Spend()
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if got := b.Remaining(); got != 2 {
		t.Fatalf("remaining = %d, want 2", got)
	}
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	b := New(10)
	ctx, cancel := NewContext(context.Background(), b)
	defer cancel()
	calls := 0
	err := Do(ctx, Policy{MaxRetries: 5}, func(_ context.Context, tok *Token) error {
		calls++
		tok.Spend()
		if calls < 3 {
			return errBoom
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if got := b.Remaining(); got != 7 {
		t.Fatalf("remaining = %d, want 7", got)
	}
}

// TestThreeLayerAttemptCap is the core guarantee: three nested retry
// layers, each allowed 3 retries (up to 4 attempts => 64 requests if
// independent), must not exceed the shared budget of 5 issued requests.
func TestThreeLayerAttemptCap(t *testing.T) {
	const maxAttempts = 5
	b := New(maxAttempts)
	ctx, cancel := NewContext(context.Background(), b)
	defer cancel()

	var requests int64
	policy := Policy{MaxRetries: 3}
	inner := Policy{MaxRetries: 3}

	err := Do(ctx, policy, func(ctx context.Context, _ *Token) error {
		return Do(ctx, policy, func(ctx context.Context, _ *Token) error {
			return Do(ctx, inner, alwaysFailTransport(&requests))
		})
	})
	if err == nil {
		t.Fatal("Do succeeded, want failure")
	}
	if !IsBudgetError(err) {
		t.Fatalf("err = %v, want a budget error", err)
	}
	if got := atomic.LoadInt64(&requests); got != maxAttempts {
		t.Fatalf("issued requests = %d, want exactly %d (budget cap)", got, maxAttempts)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining = %d, want 0", got)
	}
}

// TestCancelRefundsReservedToken covers cancellation while a retry is
// reserved but not yet used (waiting in backoff): the token must be
// returned to the budget.
func TestCancelRefundsReservedToken(t *testing.T) {
	b := New(2)
	base, cancel := context.WithCancel(context.Background())
	ctx, stop := NewContext(base, b)
	defer stop()

	opCalls := 0
	done := make(chan error, 1)
	go func() {
		done <- Do(ctx, Policy{
			MaxRetries: 5,
			Backoff:    func(int) time.Duration { return 5 * time.Second },
		}, func(_ context.Context, tok *Token) error {
			opCalls++
			tok.Spend()
			return errBoom
		})
	}()

	// Let the first attempt fail, then cancel during the backoff wait.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Do did not return after cancellation")
	}
	if opCalls != 1 {
		t.Fatalf("op calls = %d, want 1", opCalls)
	}
	// One token spent by the failed attempt; the reservation for the
	// cancelled retry must have been refunded.
	if got := b.Remaining(); got != 1 {
		t.Fatalf("remaining = %d, want 1 (reserved token refunded)", got)
	}
}

// TestBudgetDeadlineStopsRetry verifies that the budget deadline,
// propagated through the context, aborts a layer waiting in backoff
// and refunds the reservation.
func TestBudgetDeadlineStopsRetry(t *testing.T) {
	b := New(5).WithDeadline(time.Now().Add(150 * time.Millisecond))
	ctx, cancel := NewContext(context.Background(), b)
	defer cancel()

	opCalls := 0
	start := time.Now()
	err := Do(ctx, Policy{
		MaxRetries: 10,
		Backoff:    func(int) time.Duration { return 10 * time.Second },
	}, func(_ context.Context, tok *Token) error {
		opCalls++
		tok.Spend()
		return errBoom
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Do took %v, want it to abort near the budget deadline", elapsed)
	}
	if opCalls != 1 {
		t.Fatalf("op calls = %d, want 1", opCalls)
	}
	if got := b.Remaining(); got != 4 {
		t.Fatalf("remaining = %d, want 4 (reservation refunded)", got)
	}
}

func TestNonRetryableErrorStopsImmediately(t *testing.T) {
	b := New(5)
	ctx, cancel := NewContext(context.Background(), b)
	defer cancel()
	calls := 0
	err := Do(ctx, Policy{
		MaxRetries: 5,
		Retryable:  func(error) bool { return false },
	}, func(_ context.Context, tok *Token) error {
		calls++
		tok.Spend()
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestIntermediateLayerReservationRefunded ensures layers that never
// issue a request themselves do not consume budget.
func TestIntermediateLayerReservationRefunded(t *testing.T) {
	b := New(1)
	ctx, cancel := NewContext(context.Background(), b)
	defer cancel()
	var requests int64
	err := Do(ctx, Policy{MaxRetries: 2}, func(ctx context.Context, _ *Token) error {
		return Do(ctx, Policy{MaxRetries: 2}, alwaysFailTransport(&requests))
	})
	if err == nil {
		t.Fatal("Do succeeded, want failure")
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("issued requests = %d, want 1", got)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining = %d, want 0", got)
	}
}
