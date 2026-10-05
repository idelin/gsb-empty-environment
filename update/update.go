package update

import (
	"context"
	"errors"
)

// ErrTooManyConflicts is returned when the update keeps conflicting past
// the configured retry bound.
var ErrTooManyConflicts = errors.New("update: too many conflicts")

// Options controls the retry behavior of Update.
type Options struct {
	// MaxRetries bounds how many times an update is re-executed after a
	// conflict. Zero means a single attempt.
	MaxRetries int
}

// UpdateFunc computes the next value and the external side effects of the
// update from the current value. It may be executed multiple times when
// conflicts occur, so it must be free of external side effects itself; the
// returned effects are delivered by the library exactly once, after the
// write commits.
type UpdateFunc func(current []byte) (next []byte, effects []Effect, err error)

// Update runs fn against key with bounded conflict retries. fn's effects
// are enqueued into the outbox (and delivery is attempted) only after the
// conditional write succeeds, so effects from failed attempts are never
// delivered. Delivery failures leave retryable records in the outbox and do
// not fail the committed write.
func Update(ctx context.Context, s Store, ob *Outbox, key string, fn UpdateFunc, opts Options) error {
	for attempt := 0; ; attempt++ {
		current, version, err := s.Read(ctx, key)
		if err != nil {
			return err
		}
		next, effects, err := fn(current)
		if err != nil {
			return err
		}
		_, err = s.CompareAndSwap(ctx, key, next, version)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				if attempt >= opts.MaxRetries {
					return ErrTooManyConflicts
				}
				continue
			}
			return err
		}
		// Write committed: only now may side effects be delivered.
		ob.enqueue(ctx, effects)
		return nil
	}
}
