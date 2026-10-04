package budget

import (
	"context"
	"fmt"
	"time"
)

// Policy controls how a single retry layer behaves.
type Policy struct {
	// MaxRetries is the number of retries after the initial attempt.
	MaxRetries int
	// Backoff returns the wait before retry attempt n (n starts at 1).
	// A nil or zero-returning Backoff retries immediately.
	Backoff func(n int) time.Duration
	// Retryable classifies operation errors. A nil Retryable retries
	// every error. Budget errors are never retried regardless.
	Retryable func(err error) bool
}

// Do runs op with retries according to p, sharing the Budget carried
// by ctx (see NewContext) with every nested Do layer.
//
// Each attempt needs a reservation. If a usable token already flows
// through ctx (reserved by an outer layer for this same logical
// attempt), it is reused; otherwise a new token is acquired and
// attached to the context passed to op, so nested layers reuse it
// instead of reserving one token per layer. A token is charged only
// when op calls Spend — i.e. when a request is truly issued. An
// unspent token owned by this layer is refunded when the attempt ends,
// including when the attempt is cancelled while waiting for backoff.
//
// op receives the attempt's token (nil when no budget is configured).
// Layers that merely delegate to an inner layer should ignore it.
func Do(ctx context.Context, p Policy, op func(ctx context.Context, tok *Token) error) error {
	b, _ := FromContext(ctx)
	var lastErr error
	for attempt := 0; ; attempt++ {
		var tok *Token
		owned := false
		if b != nil {
			tok = tokenFromContext(ctx)
			if tok == nil || !tok.Reserved() {
				t, err := b.Acquire()
				if err != nil {
					if lastErr != nil {
						return fmt.Errorf("retry: %w (last attempt failed: %v)", err, lastErr)
					}
					return err
				}
				tok = t
				owned = true
				ctx = contextWithToken(ctx, tok)
			}
		}
		if attempt > 0 && p.Backoff != nil {
			if wait := p.Backoff(attempt); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					releaseOwned(tok, owned)
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		// Cancelled (or budget deadline propagated via context) before
		// the request could be issued: refund the reservation.
		if err := ctx.Err(); err != nil {
			releaseOwned(tok, owned)
			return err
		}
		err := op(ctx, tok)
		releaseOwned(tok, owned) // no-op if op spent it
		if err == nil {
			return nil
		}
		lastErr = err
		if IsBudgetError(err) {
			return err
		}
		if p.Retryable != nil && !p.Retryable(err) {
			return err
		}
		if attempt >= p.MaxRetries {
			return err
		}
	}
}

func releaseOwned(tok *Token, owned bool) {
	if owned && tok != nil {
		tok.Release()
	}
}
