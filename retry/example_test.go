package retry_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"prescreen.local/ps-9f073c0c88/retry"
)

// ExampleExecutor shows a sidecar wiring the runtime decision maker to a
// flaky downstream: the first two attempts fail, the third succeeds, and the
// retry budget / breaker / deadline are all enforced automatically.
func ExampleExecutor() {
	ctrl := retry.NewEdgeController(retry.EdgeConfig{
		MaxAttempts:   4,
		MaxHedges:     0,
		MinProcessing: time.Millisecond,
		Backoff: retry.BackoffConfig{
			BaseDelay: time.Millisecond, Multiplier: 2, MaxDelay: 20 * time.Millisecond,
		},
		Budget: retry.RetryBudgetConfig{
			BudgetRatio: 0.2, MinRetryConcurrency: 2, Window: time.Second,
		},
		Breaker: retry.BreakerConfig{FailureThreshold: 10, OpenDuration: time.Second},
	}, retry.NewRealScheduler())

	failuresLeft := 2
	invoke := func(_ context.Context, attempt int, _ bool) error {
		if failuresLeft > 0 {
			failuresLeft--
			return errors.New("transient overload")
		}
		return nil
	}
	exec := retry.NewExecutor(ctrl, retry.NewRealScheduler(), invoke, context.Background())
	out := exec.Execute(retry.Deadline{})
	fmt.Println("success:", out.Success)
	fmt.Println("attempts:", out.Attempts)
	fmt.Println("abort:", out.Abort)
	// Output:
	// success: true
	// attempts: 3
	// abort: None
}
