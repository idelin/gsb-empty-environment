package pubsub

import (
	"context"
	"time"
)

// TaskFunc processes one message. An error is treated as a failure and,
// unless the attempt budget is exhausted, retried after a backoff. Tests
// inject their own TaskFunc to drive deterministic successes and failures.
type TaskFunc[T any] func(ctx context.Context, msg T) error

// FailEvent describes a single failed handler invocation.
type FailEvent[T any] struct {
	Msg     T
	Err     error
	Attempt int // 1-based number of the attempt that just failed
	Final   bool
}

// WorkerConfig configures Serve.
type WorkerConfig[T any] struct {
	// Handler is the task executed for every message. Required.
	Handler TaskFunc[T]
	// MaxAttempts is the per-message attempt budget. Defaults to 1.
	MaxAttempts int
	// InitialBackoff is the delay before the second attempt. It doubles up
	// to MaxBackoff. Defaults to 10ms / 1s.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// Clock injects time. Defaults to the wall clock; tests pass a fake.
	Clock Clock
	// OnFailure, if set, runs after each failed attempt (Final marks the
	// last allowed attempt).
	OnFailure func(FailEvent[T])
}

// WorkerStats summarizes a Serve run.
type WorkerStats struct {
	Succeeded int64 // messages handled without error
	Failed    int64 // messages that exhausted their attempt budget
	Attempts  int64 // total handler invocations
}

// ErrWrongType is reported when a queue value cannot be cast to T.
type ErrWrongType struct{ Value any }

func (e ErrWrongType) Error() string { return "pubsub: message is not of the expected type" }

// Serve consumes sub and runs cfg.Handler for each message until ctx is
// cancelled or the subscription is Done. When Done fires, all messages
// already committed to the queue are processed first (drain semantics),
// then Serve returns its stats.
func Serve[T any](ctx context.Context, sub *Subscription, cfg WorkerConfig[T]) WorkerStats {
	if cfg.Handler == nil {
		panic("pubsub: Serve requires a Handler")
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	initial := cfg.InitialBackoff
	if initial <= 0 {
		initial = 10 * time.Millisecond
	}
	maximum := cfg.MaxBackoff
	if maximum <= 0 {
		maximum = time.Second
	}
	clock := cfg.Clock
	if clock == nil {
		clock = WallClock{}
	}

	var stats WorkerStats

	var process func(msg T, runCtx context.Context)
	process = func(msg T, runCtx context.Context) {
		backoff := initial
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			stats.Attempts++
			err := cfg.Handler(runCtx, msg)
			if err == nil {
				stats.Succeeded++
				return
			}
			if cfg.OnFailure != nil {
				cfg.OnFailure(FailEvent[T]{Msg: msg, Err: err, Attempt: attempt, Final: attempt == maxAttempts})
			}
			if attempt == maxAttempts {
				stats.Failed++
				return
			}
			if !wait(runCtx, clock, backoff) {
				return // cancelled while waiting; message stays accounted via Attempts
			}
			backoff *= 2
			if backoff > maximum || backoff <= 0 {
				backoff = maximum
			}
		}
	}

	drain := func() WorkerStats {
		for _, raw := range sub.Drain() {
			v, ok := raw.(T)
			if !ok {
				stats.Attempts++
				stats.Failed++
				if cfg.OnFailure != nil {
					cfg.OnFailure(FailEvent[T]{Err: ErrWrongType{Value: raw}, Attempt: 1, Final: true})
				}
				continue
			}
			process(v, context.WithoutCancel(ctx))
		}
		return stats
	}

	for {
		select {
		case <-ctx.Done():
			return stats
		case <-sub.Done():
			return drain()
		case raw := <-sub.C():
			v, ok := raw.(T)
			if !ok {
				stats.Attempts++
				stats.Failed++
				if cfg.OnFailure != nil {
					cfg.OnFailure(FailEvent[T]{Err: ErrWrongType{Value: raw}, Attempt: 1, Final: true})
				}
				continue
			}
			process(v, ctx)
		}
	}
}

// wait blocks for d using the injected clock, returning false if ctx ends.
func wait(ctx context.Context, clock Clock, d time.Duration) bool {
	t := clock.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C():
		return true
	}
}
