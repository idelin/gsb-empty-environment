package retry

import (
	"testing"
	"time"
)

func testEdgeConfig() EdgeConfig {
	return EdgeConfig{
		MaxAttempts:   4,
		MaxHedges:     2,
		HedgeDelay:    5 * time.Millisecond,
		MinProcessing: 5 * time.Millisecond,
		Backoff: BackoffConfig{
			BaseDelay: time.Millisecond, Multiplier: 2, MaxDelay: 50 * time.Millisecond,
		},
		Budget: RetryBudgetConfig{
			BudgetRatio: 0.2, MinRetryConcurrency: 1, Window: time.Second,
		},
		Breaker: BreakerConfig{FailureThreshold: 5, OpenDuration: time.Second},
	}
}

func TestControllerDeadlineTruncatesRetry(t *testing.T) {
	clk := NewSimScheduler()
	ctrl := NewEdgeController(testEdgeConfig(), clk)
	start := clk.Now()
	// Deadline leaves only 6ms; backoff(1ms) + minProc(5ms) = 6ms, and the
	// pre-check is strictly greater-than, so the retry must be aborted.
	dl := NewDeadline(start, 6*time.Millisecond)
	callID, begin := ctrl.BeginCall(start, dl)
	if !begin.Allowed {
		t.Fatal("initial attempt must be admitted")
	}
	ctrl.AttemptFinished(1, false, false, false)
	dec := ctrl.DecideRetry(callID, 1, dl, true)
	if dec.Allowed || dec.Reason != AbortDeadline {
		t.Fatalf("expected deadline abort, got allowed=%v reason=%v", dec.Allowed, dec.Reason)
	}
	if st := ctrl.Stats(); st.DeadlineAborts != 1 {
		t.Fatalf("deadline abort counter = %d, want 1", st.DeadlineAborts)
	}
}

func TestControllerBudgetRejectsRetry(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.Budget.MinRetryConcurrency = 0
	cfg.Budget.BudgetRatio = 0 // no retry slots ever
	ctrl := NewEdgeController(cfg, clk)
	dl := Deadline{}
	callID, begin := ctrl.BeginCall(clk.Now(), dl)
	if !begin.Allowed {
		t.Fatal("initial attempt must be admitted")
	}
	ctrl.AttemptFinished(1, false, false, false)
	dec := ctrl.DecideRetry(callID, 1, dl, true)
	if dec.Allowed || dec.Reason != AbortBudgetExhausted {
		t.Fatalf("expected budget abort, got %+v", dec)
	}
}

func TestControllerMaxAttempts(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.Budget.BudgetRatio = -1 // unlimited
	ctrl := NewEdgeController(cfg, clk)
	dl := Deadline{}
	callID, _ := ctrl.BeginCall(clk.Now(), dl)
	used := 1
	for {
		dec := ctrl.DecideRetry(callID, used, dl, true)
		if !dec.Allowed {
			if used != cfg.MaxAttempts || dec.Reason != AbortMaxAttempts {
				t.Fatalf("stop at used=%d reason=%v", used, dec.Reason)
			}
			break
		}
		used = dec.Attempt
		ctrl.AttemptFinished(dec.Attempt, true, false, false)
	}
	if used != 4 {
		t.Fatalf("dispatched %d attempts, want 4", used)
	}
}

func TestControllerHedgeBudgetAndCancellation(t *testing.T) {
	clk := NewSimScheduler()
	cfg := testEdgeConfig()
	cfg.Budget.BudgetRatio = -1
	ctrl := NewEdgeController(cfg, clk)
	dl := Deadline{}
	callID, _ := ctrl.BeginCall(clk.Now(), dl)
	d1 := ctrl.DecideHedge(callID, 0, 1, dl)
	if !d1.Allowed || !d1.IsHedge || d1.Attempt != 2 {
		t.Fatalf("first hedge decision wrong: %+v", d1)
	}
	d2 := ctrl.DecideHedge(callID, 1, 2, dl)
	if !d2.Allowed || d2.Attempt != 3 {
		t.Fatalf("second hedge decision wrong: %+v", d2)
	}
	d3 := ctrl.DecideHedge(callID, 2, 3, dl)
	if d3.Allowed || d3.Reason != AbortMaxAttempts {
		t.Fatalf("third hedge must exceed MaxHedges, got %+v", d3)
	}
	// Cancel the two hedge branches; their slots return to the budget.
	ctrl.CancelAttempt(2, true)
	ctrl.CancelAttempt(3, true)
	if st := ctrl.Stats(); st.RetryInFlight != 0 {
		t.Fatalf("retry in flight after cancel = %d, want 0", st.RetryInFlight)
	}
}

func TestControllerNonRetryableFailure(t *testing.T) {
	clk := NewSimScheduler()
	ctrl := NewEdgeController(testEdgeConfig(), clk)
	callID, _ := ctrl.BeginCall(clk.Now(), Deadline{})
	dec := ctrl.DecideRetry(callID, 1, Deadline{}, false)
	if dec.Allowed || dec.Reason != AbortNonRetryable {
		t.Fatalf("non-retryable failure must abort, got %+v", dec)
	}
}
