package tasksched

import (
	"context"
	"time"
)

// TaskFunc is injected by the caller. attempt is 1-based.
type TaskFunc func(ctx context.Context, attempt int) error

// Attempt records one execution of the task.
type Attempt struct {
	Number    int
	StartedAt time.Time
	Err       error
}

// Outcome carries the full attempt history for a task ID.
type Outcome struct {
	TaskID   string
	Attempts []Attempt
}

// Run executes fn for taskID, retrying failures according to backoff.
//
//   - Every retry reuses taskID with an incremented attempt number.
//   - backoff[i] is the wait before attempt i+2; the last entry repeats
//     if attempts outnumber the sequence.
//   - Cancellation during a backoff wait returns ctx.Err() immediately,
//     without waiting for the timer; the timer is stopped so it can
//     never fire late.
//   - A cancel/expiry race resolves to exactly one result via select.
//   - When maxAttempts is exhausted, the last error is returned along
//     with the complete attempt history in the Outcome.
func Run(ctx context.Context, clk Clock, taskID string, maxAttempts int, backoff []time.Duration, fn TaskFunc) (Outcome, error) {
	out := Outcome{TaskID: taskID}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		a := Attempt{Number: attempt, StartedAt: clk.Now()}
		a.Err = fn(ctx, attempt)
		out.Attempts = append(out.Attempts, a)
		if a.Err == nil {
			return out, nil
		}
		if attempt == maxAttempts {
			return out, a.Err
		}
		d := backoff[min(attempt-1, len(backoff)-1)]
		timer := clk.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C():
		}
	}
	return out, nil // unreachable: loop always returns
}
