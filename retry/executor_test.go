package retry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingInvoke lets a test decide per-attempt outcomes and synchronize
// in-flight hedge branches.
type blockingInvoke struct {
	mu          sync.Mutex
	failFirstN  int
	attempts    int32
	concurrent  int32
	maxConc     int32
	slow        time.Duration
	clk         *SimScheduler
	release     chan struct{}
	blockHedges bool
}

func (b *blockingInvoke) fn() InvokeFunc {
	return func(ctx context.Context, attempt int, isHedge bool) error {
		cur := atomic.AddInt32(&b.concurrent, 1)
		for {
			old := atomic.LoadInt32(&b.maxConc)
			if cur <= old || atomic.CompareAndSwapInt32(&b.maxConc, old, cur) {
				break
			}
		}
		defer atomic.AddInt32(&b.concurrent, -1)
		atomic.AddInt32(&b.attempts, 1)

		if b.blockHedges && isHedge {
			// Block until cancelled by the winning initial attempt.
			<-ctx.Done()
			return ctx.Err()
		}
		if b.slow > 0 {
			select {
			case <-b.clk.After(b.slow):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		b.mu.Lock()
		fail := b.failFirstN > 0
		if fail {
			b.failFirstN--
		}
		b.mu.Unlock()
		if fail {
			return errors.New("downstream failed")
		}
		return nil
	}
}

func TestExecutorRetriesThenSucceeds(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.Budget.BudgetRatio = -1
	cfg.Backoff.BaseDelay = time.Millisecond
	ctrl := NewEdgeController(cfg, clk)
	inv := &blockingInvoke{failFirstN: 2}
	exec := NewExecutor(ctrl, clk, inv.fn(), context.Background())

	done := make(chan Outcome, 1)
	go func() { done <- exec.Execute(Deadline{}) }()

	// Drive virtual time until the call completes (3 attempts, 2 backoffs).
	deadline := time.Now().Add(5 * time.Second)
	for {
		clk.RunFor(500 * time.Microsecond)
		select {
		case out := <-done:
			if !out.Success {
				t.Fatalf("expected eventual success, got %+v", out)
			}
			if out.Attempts != 3 {
				t.Fatalf("attempts = %d, want 3", out.Attempts)
			}
			if atomic.LoadInt32(&inv.attempts) != 3 {
				t.Fatalf("invoke count = %d, want 3", inv.attempts)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("test timed out; executor never completed")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestExecutorHedgeRacingCancellation(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.MaxAttempts = 3
	cfg.MaxHedges = 2
	cfg.HedgeDelay = 2 * time.Millisecond
	cfg.Budget.BudgetRatio = -1
	ctrl := NewEdgeController(cfg, clk)
	inv := &blockingInvoke{slow: 5 * time.Millisecond, blockHedges: true, clk: clk}
	exec := NewExecutor(ctrl, clk, inv.fn(), context.Background())

	done := make(chan Outcome, 1)
	go func() { done <- exec.Execute(Deadline{}) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		clk.RunFor(200 * time.Microsecond)
		select {
		case out := <-done:
			if !out.Success {
				t.Fatalf("expected success, got %+v", out)
			}
			if out.Hedges < 1 {
				t.Fatalf("expected at least one hedge, got %d", out.Hedges)
			}
			st := ctrl.Stats()
			if st.RetryInFlight != 0 || st.ActiveNormal != 0 {
				t.Fatalf("bookkeeping leaked: %+v", st)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("hedge test timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestExecutorDeadlineStopsRetry(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.Budget.BudgetRatio = -1
	cfg.Backoff.BaseDelay = 10 * time.Millisecond
	cfg.MinProcessing = 5 * time.Millisecond
	ctrl := NewEdgeController(cfg, clk)
	inv := &blockingInvoke{failFirstN: 100} // always fail
	exec := NewExecutor(ctrl, clk, inv.fn(), context.Background())

	dl := NewDeadline(clk.Now(), 12*time.Millisecond)
	done := make(chan Outcome, 1)
	go func() { done <- exec.Execute(dl) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		clk.RunFor(time.Millisecond)
		select {
		case out := <-done:
			if out.Success {
				t.Fatal("call must not succeed under permanent failure")
			}
			if out.Abort != AbortDeadline && out.Abort != AbortMaxAttempts {
				t.Fatalf("expected deadline/max-attempts abort, got %v", out.Abort)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("deadline test timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestExecutorBreakerRejectsUpfront(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.Breaker.FailureThreshold = 1
	ctrl := NewEdgeController(cfg, clk)
	now := clk.Now()
	ctrl.Breaker().RecordFailure(now)
	exec := NewExecutor(ctrl, clk, func(context.Context, int, bool) error {
		t.Fatal("invoke must not run when breaker is open")
		return nil
	}, context.Background())
	out := exec.Execute(Deadline{})
	if out.Success || out.Abort != AbortBreakerOpen {
		t.Fatalf("expected breaker-open rejection, got %+v", out)
	}
}
