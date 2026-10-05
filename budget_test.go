package retrybudget

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errAlwaysFails = errors.New("request failed")

// TestThreeLayerSharedBudget simulates three nested retry layers, each
// of which would happily retry forever. The shared budget must cap the
// total number of issued requests instead of letting them multiply.
func TestThreeLayerSharedBudget(t *testing.T) {
	const maxAttempts = 5
	b := New(maxAttempts)
	ctx := ContextWithBudget(context.Background(), b)

	var issued int64
	// Leaf: the only place a real request goes out, charged via Issue.
	leaf := func(ctx context.Context) error {
		return Issue(ctx, func(ctx context.Context) error {
			atomic.AddInt64(&issued, 1)
			return errAlwaysFails
		})
	}
	layer3 := func(ctx context.Context) error { return Do(ctx, leaf) }
	layer2 := func(ctx context.Context) error { return Do(ctx, layer3) }
	layer1 := func(ctx context.Context) error { return Do(ctx, layer2) }

	err := Do(ctx, layer1)
	if err != ErrBudgetExhausted {
		t.Fatalf("expected ErrBudgetExhausted, got %v", err)
	}
	if got := atomic.LoadInt64(&issued); got != maxAttempts {
		t.Fatalf("issued %d requests, want exactly %d", got, maxAttempts)
	}
	if got := b.Spent(); got != maxAttempts {
		t.Fatalf("budget spent %d, want %d", got, maxAttempts)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("budget remaining %d, want 0", got)
	}
}

// TestIndependentLayersWouldMultiply documents the failure mode the
// budget prevents: three layers each retrying up to n times on their
// own would issue n^3 requests.
func TestIndependentLayersWouldMultiply(t *testing.T) {
	const perLayer = 3
	var issued int64
	leaf := func() error { atomic.AddInt64(&issued, 1); return errAlwaysFails }
	retry := func(n int, fn func() error) {
		for i := 0; i < n; i++ {
			if fn() == nil {
				return
			}
		}
	}
	retry(perLayer, func() error {
		retry(perLayer, func() error {
			retry(perLayer, leaf)
			return errAlwaysFails
		})
		return errAlwaysFails
	})
	if got := atomic.LoadInt64(&issued); got != perLayer*perLayer*perLayer {
		t.Fatalf("baseline sanity check failed: issued %d", got)
	}
}

// TestCancelledAttemptRefundsToken covers a token that was reserved but
// never used because the context was cancelled before the request went
// out: the token must return to the shared pool.
func TestCancelledAttemptRefundsToken(t *testing.T) {
	b := New(1)
	ctx, cancel := context.WithCancel(context.Background())
	ctx = ContextWithBudget(ctx, b)

	tok, err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining %d, want 0 while token reserved", got)
	}
	cancel()
	// The attempt is abandoned before any request is issued.
	if !tok.Refund() {
		t.Fatal("refund of unused token failed")
	}
	if got := b.Remaining(); got != 1 {
		t.Fatalf("remaining %d, want 1 after refund", got)
	}
	if got := b.Spent(); got != 0 {
		t.Fatalf("spent %d, want 0 (no request was issued)", got)
	}
}

// TestIssueRefundsWhenCancelledBeforeIssue verifies Issue never charges
// the budget for a request that cancellation prevented.
func TestIssueRefundsWhenCancelledBeforeIssue(t *testing.T) {
	b := New(3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already dead before the first attempt
	ctx = ContextWithBudget(ctx, b)

	var issued int64
	err := Do(ctx, func(ctx context.Context) error {
		return Issue(ctx, func(ctx context.Context) error {
			atomic.AddInt64(&issued, 1)
			return errAlwaysFails
		})
	})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if issued != 0 || b.Spent() != 0 {
		t.Fatalf("issued=%d spent=%d, want 0/0", issued, b.Spent())
	}
	if got := b.Remaining(); got != 3 {
		t.Fatalf("remaining %d, want 3 (no token leaked)", got)
	}
}

// TestTokenResolvedExactlyOnce ensures a token cannot be spent twice or
// refunded after being spent.
func TestTokenResolvedExactlyOnce(t *testing.T) {
	b := New(1)
	tok, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !tok.Spend() {
		t.Fatal("first Spend should succeed")
	}
	if tok.Spend() {
		t.Fatal("second Spend must fail")
	}
	if tok.Refund() {
		t.Fatal("Refund after Spend must fail")
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("remaining %d, want 0", got)
	}
}

// TestBudgetDeadlineStopsAcquire checks that an expired budget refuses
// new tokens and that the deadline propagates into the context.
func TestBudgetDeadlineStopsAcquire(t *testing.T) {
	expired := New(5, WithDeadline(time.Now().Add(-time.Second)))
	if _, err := expired.Acquire(context.Background()); err != ErrDeadlineExceeded {
		t.Fatalf("expected ErrDeadlineExceeded, got %v", err)
	}

	b := New(5, WithTimeout(50*time.Millisecond))
	ctx := ContextWithBudget(context.Background(), b)
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("budget deadline not propagated to context")
	}
	if bd, _ := b.Deadline(); !dl.Equal(bd) {
		t.Fatalf("context deadline %v != budget deadline %v", dl, bd)
	}
	<-ctx.Done()
	if _, err := b.Acquire(ctx); err == nil {
		t.Fatal("acquire after deadline should fail")
	}
}

// TestParentDeadlineWins ensures an earlier parent deadline is kept.
func TestParentDeadlineWins(t *testing.T) {
	parentDL := time.Now().Add(time.Second)
	parent, cancel := context.WithDeadline(context.Background(), parentDL)
	defer cancel()
	b := New(5, WithTimeout(time.Hour))
	ctx := ContextWithBudget(parent, b)
	dl, _ := ctx.Deadline()
	if !dl.Equal(parentDL) {
		t.Fatalf("context deadline %v, want parent deadline %v", dl, parentDL)
	}
}

// TestConcurrentAcquireNeverExceedsMax hammers the budget from many
// goroutines and checks the invariant that spent never exceeds max.
func TestConcurrentAcquireNeverExceedsMax(t *testing.T) {
	const max = 10
	b := New(max)
	var spent int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				tok, err := b.Acquire(context.Background())
				if err == ErrBudgetExhausted {
					return
				}
				if err != nil {
					t.Errorf("unexpected acquire error: %v", err)
					return
				}
				tok.Spend()
				atomic.AddInt64(&spent, 1)
			}
		}()
	}
	wg.Wait()
	if spent != max || b.Spent() != max {
		t.Fatalf("spent %d (budget %d), want exactly %d", spent, b.Spent(), max)
	}
}
