// Package retrybudget provides a shared attempt budget that can be
// propagated across layered retry logic so that the total number of
// requests issued by nested retry loops stays capped, instead of
// multiplying at every layer.
//
// A Budget hands out tokens. A token is only "spent" when the caller
// actually issues the request; if the attempt is abandoned before the
// request goes out (e.g. the context is cancelled), the token must be
// refunded so another layer can use it.
package retrybudget

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Errors returned by Acquire.
var (
	// ErrBudgetExhausted means the shared attempt budget has no tokens left.
	ErrBudgetExhausted = errors.New("retrybudget: attempt budget exhausted")
	// ErrDeadlineExceeded means the budget deadline has already passed.
	ErrDeadlineExceeded = errors.New("retrybudget: budget deadline exceeded")
)

// Token states.
const (
	tokenReserved int32 = iota
	tokenSpent
	tokenRefunded
)

// Budget is a shared, goroutine-safe cap on the total number of request
// attempts that may be issued across any number of retry layers.
type Budget struct {
	max      int64
	inflight int64 // reserved + spent tokens
	spent    int64 // tokens actually consumed by issued requests
	deadline time.Time
	hasDL    bool
}

// Option configures a Budget.
type Option func(*Budget)

// WithDeadline sets an absolute deadline on the budget. Acquiring a
// token after the deadline fails, and ContextWithBudget propagates the
// deadline into the returned context.
func WithDeadline(d time.Time) Option {
	return func(b *Budget) {
		b.deadline = d
		b.hasDL = true
	}
}

// WithTimeout sets a deadline relative to now.
func WithTimeout(d time.Duration) Option {
	return WithDeadline(time.Now().Add(d))
}

// New creates a Budget allowing at most maxAttempts issued requests.
func New(maxAttempts int64, opts ...Option) *Budget {
	b := &Budget{max: maxAttempts}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Max returns the configured attempt cap.
func (b *Budget) Max() int64 { return b.max }

// Spent returns how many tokens have been consumed by issued requests.
func (b *Budget) Spent() int64 { return atomic.LoadInt64(&b.spent) }

// Remaining returns how many more requests may still be issued.
func (b *Budget) Remaining() int64 {
	return b.max - atomic.LoadInt64(&b.inflight)
}

// Deadline reports the budget deadline, if any.
func (b *Budget) Deadline() (time.Time, bool) { return b.deadline, b.hasDL }

// Acquire reserves a token for one attempt. It fails if the budget is
// exhausted, the budget deadline has passed, or ctx is already done.
//
// The caller must later call exactly one of Spend or Refund on the
// returned token.
func (b *Budget) Acquire(ctx context.Context) (*Token, error) {
	if b.hasDL && !time.Now().Before(b.deadline) {
		return nil, ErrDeadlineExceeded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		cur := atomic.LoadInt64(&b.inflight)
		if cur >= b.max {
			return nil, ErrBudgetExhausted
		}
		if atomic.CompareAndSwapInt64(&b.inflight, cur, cur+1) {
			return &Token{budget: b}, nil
		}
	}
}

// Token represents one reserved attempt. It must be resolved exactly
// once: Spend when the request is actually issued, Refund when the
// attempt is abandoned without issuing a request.
type Token struct {
	budget *Budget
	state  int32
}

// Spend marks the token as consumed by an issued request. It returns
// false if the token was already resolved.
func (t *Token) Spend() bool {
	if !atomic.CompareAndSwapInt32(&t.state, tokenReserved, tokenSpent) {
		return false
	}
	atomic.AddInt64(&t.budget.spent, 1)
	return true
}

// Refund returns an unused token to the budget so another attempt (in
// any layer) may use it. It returns false if the token was already
// resolved.
func (t *Token) Refund() bool {
	if !atomic.CompareAndSwapInt32(&t.state, tokenReserved, tokenRefunded) {
		return false
	}
	atomic.AddInt64(&t.budget.inflight, -1)
	return true
}

type ctxKey struct{}

// ContextWithBudget attaches b to ctx and propagates the budget
// deadline: the returned context is cancelled at the earlier of the
// parent's deadline and the budget deadline.
func ContextWithBudget(ctx context.Context, b *Budget) context.Context {
	ctx = context.WithValue(ctx, ctxKey{}, b)
	if b.hasDL {
		if parentDL, ok := ctx.Deadline(); !ok || b.deadline.Before(parentDL) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, b.deadline)
			_ = cancel // lifetime tied to parent; budget deadline is final
		}
	}
	return ctx
}

// FromContext returns the Budget attached to ctx, if any.
func FromContext(ctx context.Context) (*Budget, bool) {
	b, ok := ctx.Value(ctxKey{}).(*Budget)
	return b, ok
}
