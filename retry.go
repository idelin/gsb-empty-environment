package retrybudget

import "context"

// Issue charges the shared budget for exactly one real request and then
// sends it. This is the only place tokens are spent: intermediate retry
// layers built with Do never consume budget, so nesting layers cannot
// multiply the total number of issued requests.
//
// If the context is cancelled after the token was reserved but before
// the request goes out, the token is refunded to the shared budget.
//
// Without a budget in ctx, Issue simply calls send.
func Issue(ctx context.Context, send func(ctx context.Context) error) error {
	b, ok := FromContext(ctx)
	if !ok {
		return send(ctx)
	}
	tok, err := b.Acquire(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		tok.Refund()
		return err
	}
	tok.Spend()
	return send(ctx)
}

// Do runs fn, retrying while fn returns a retryable error. It stops and
// returns the error as soon as fn succeeds, the shared budget is
// exhausted (ErrBudgetExhausted), the budget deadline passes
// (ErrDeadlineExceeded), or the context is done.
//
// Do itself never spends budget; only requests issued through Issue are
// charged. Without a budget in ctx, Do runs fn exactly once: retrying
// requires a shared budget so attempts stay accountable.
func Do(ctx context.Context, fn func(ctx context.Context) error) error {
	_, hasBudget := FromContext(ctx)
	for {
		err := fn(ctx)
		if err == nil || !Retryable(err) {
			return err
		}
		if !hasBudget {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
	}
}

// Retryable reports whether an error allows another attempt. Budget
// exhaustion, budget deadline, and context cancellation are terminal.
func Retryable(err error) bool {
	switch err {
	case nil, ErrBudgetExhausted, ErrDeadlineExceeded,
		context.Canceled, context.DeadlineExceeded:
		return false
	}
	return true
}
