package retry

import (
	"testing"
	"time"
)

func TestRetryBudgetFormula(t *testing.T) {
	b := NewRetryBudget(RetryBudgetConfig{
		BudgetRatio: 0.2, MinRetryConcurrency: 2, Window: time.Second,
	})
	now := time.Unix(0, 0)
	// 10 active normal requests: floor(0.2*10)=2, floor wins (== min).
	for i := 0; i < 10; i++ {
		b.NormalStarted(now)
	}
	if got := b.AllowedRetries(now); got != 2 {
		t.Fatalf("allowed with 10 active = %d, want 2", got)
	}
	// Ratio slot dominates once active traffic is large.
	for i := 0; i < 15; i++ {
		b.NormalStarted(now)
	}
	if got := b.AllowedRetries(now); got != 5 {
		t.Fatalf("allowed with 25 active = %d, want 5", got)
	}
}

func TestRetryBudgetMinFloorAtLowTraffic(t *testing.T) {
	b := NewRetryBudget(RetryBudgetConfig{
		BudgetRatio: 0.2, MinRetryConcurrency: 3, Window: time.Second,
	})
	now := time.Unix(0, 0)
	// Only 2 normal requests: floor(0.2*2)=0, min floor of 3 applies.
	for i := 0; i < 2; i++ {
		b.NormalStarted(now)
	}
	if got := b.AllowedRetries(now); got != 3 {
		t.Fatalf("low traffic allowed = %d, want min floor 3", got)
	}
	acquired := 0
	for b.TryAcquire(now) {
		acquired++
		if acquired > 10 {
			t.Fatal("budget did not enforce its limit")
		}
	}
	if acquired != 3 {
		t.Fatalf("acquired under low traffic = %d, want 3", acquired)
	}
	// Releasing a slot makes room for exactly one more retry.
	b.Release()
	if !b.TryAcquire(now) {
		t.Fatal("expected one slot after release")
	}
	if b.TryAcquire(now) {
		t.Fatal("expected budget to be full again")
	}
}

func TestRetryBudgetSlidingWindowEviction(t *testing.T) {
	b := NewRetryBudget(RetryBudgetConfig{
		BudgetRatio: 1, MinRetryConcurrency: 0, Window: 100 * time.Millisecond,
	})
	start := time.Unix(0, 0)
	for i := 0; i < 5; i++ {
		b.NormalStarted(start)
	}
	if got := b.WindowCount(start.Add(50 * time.Millisecond)); got != 5 {
		t.Fatalf("window count before expiry = %d, want 5", got)
	}
	if got := b.WindowCount(start.Add(150 * time.Millisecond)); got != 0 {
		t.Fatalf("window count after expiry = %d, want 0", got)
	}
}

func TestRetryBudgetUnlimited(t *testing.T) {
	b := NewRetryBudget(RetryBudgetConfig{BudgetRatio: -1})
	if !b.Unlimited() {
		t.Fatal("negative ratio must mean unlimited")
	}
	for i := 0; i < 1000; i++ {
		if !b.TryAcquire(time.Unix(0, 0)) {
			t.Fatal("unlimited budget refused an attempt")
		}
	}
}
