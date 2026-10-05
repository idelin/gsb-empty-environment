package updatekv

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRetryExhausted is returned when the update keeps conflicting and
// the retry budget is spent. No side effects are delivered in this case.
var ErrRetryExhausted = errors.New("updatekv: retry budget exhausted")

// ErrNoOutbox is returned when an update function produced effects but
// no Outbox was configured to deliver them.
var ErrNoOutbox = errors.New("updatekv: effects produced but no outbox configured")

// Func computes the next value and the external side effects of an
// update from the current value. It may be executed multiple times when
// writes conflict, so it must be pure: effects are only delivered after
// the write built from the winning execution succeeds.
type Func func(current []byte) (next []byte, effects []Effect, err error)

// Options controls Run.
type Options struct {
	// MaxRetries bounds how many times a conflicted update is retried.
	// Zero means a single attempt.
	MaxRetries int
	// Backoff returns the delay before retry attempt i (0-based).
	// Defaults to an exponential backoff capped at one second.
	Backoff func(attempt int) time.Duration
	// Sleep waits for d or returns early on context cancellation.
	// Defaults to a timer-based sleep; tests can substitute a no-op.
	Sleep func(ctx context.Context, d time.Duration) error
	// Outbox receives the effects of the committed update. Required
	// when the update function returns any effects.
	Outbox *Outbox
}

// Result describes a committed update.
type Result struct {
	// Version is the record version produced by the winning write.
	Version uint64
	// Attempts is the number of write attempts (1 + conflicts retried).
	Attempts int
}

// Run executes fn against key with optimistic concurrency: read, compute
// the next value, conditionally write, retry on conflict up to
// MaxRetries. Only the effects of the winning attempt are delivered,
// exactly once, after its write succeeds. If delivery fails, Run returns
// a *DeliveryError and the effects remain as retryable records in the
// outbox.
func Run(ctx context.Context, s Store, key string, fn Func, opts Options) (Result, error) {
	backoff := opts.Backoff
	if backoff == nil {
		backoff = DefaultBackoff
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = timerSleep
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return Result{Attempts: attempt}, err
		}

		rec, err := s.Read(ctx, key)
		if err != nil {
			return Result{Attempts: attempt}, err
		}

		next, effects, err := fn(rec.Value)
		if err != nil {
			return Result{Attempts: attempt}, err
		}

		newVersion, err := s.CompareAndSwap(ctx, key, next, rec.Version)
		switch {
		case err == nil:
			res := Result{Version: newVersion, Attempts: attempt + 1}
			if len(effects) == 0 {
				return res, nil
			}
			if opts.Outbox == nil {
				return res, ErrNoOutbox
			}
			// The write is committed: enqueue first so the effects are
			// retained even if this process dies mid-delivery, then
			// try to deliver immediately.
			opts.Outbox.enqueue(effects)
			if derr := opts.Outbox.Flush(ctx); derr != nil {
				return res, derr
			}
			return res, nil
		case errors.Is(err, ErrConflict):
			if attempt >= opts.MaxRetries {
				return Result{Attempts: attempt + 1},
					fmt.Errorf("%w after %d attempt(s)", ErrRetryExhausted, attempt+1)
			}
			if err := sleep(ctx, backoff(attempt)); err != nil {
				return Result{Attempts: attempt + 1}, err
			}
		default:
			return Result{Attempts: attempt + 1}, err
		}
	}
}

// DefaultBackoff returns 10ms * 2^attempt, capped at one second.
func DefaultBackoff(attempt int) time.Duration {
	d := 10 * time.Millisecond
	for i := 0; i < attempt && d < time.Second; i++ {
		d *= 2
	}
	if d > time.Second {
		d = time.Second
	}
	return d
}

func timerSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
