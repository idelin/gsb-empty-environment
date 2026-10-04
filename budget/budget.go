// Package budget provides a retry-attempt budget that is shared across
// nested retry layers, so that the total number of requests actually
// issued is capped no matter how many layers retry independently.
//
// A Budget is carried through the call stack via context.Context. Each
// retry layer reserves a Token per attempt; the token is only charged
// (Spend) when a request is truly issued. Tokens that are reserved but
// never spent — e.g. because the attempt was cancelled while waiting
// for backoff — are released back into the budget (Release).
package budget

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrExhausted is returned by Acquire when no attempts remain.
	ErrExhausted = errors.New("budget: attempt budget exhausted")
	// ErrDeadlineExceeded is returned by Acquire when the budget
	// deadline has passed.
	ErrDeadlineExceeded = errors.New("budget: deadline exceeded")
)

// Budget is a thread-safe pool of attempt tokens with an optional
// deadline. The zero value is not usable; construct with New.
type Budget struct {
	mu        sync.Mutex
	remaining int
	deadline  time.Time // zero means no deadline
	now       func() time.Time
}

// New returns a Budget allowing up to maxAttempts charged requests.
func New(maxAttempts int) *Budget {
	return &Budget{remaining: maxAttempts, now: time.Now}
}

// WithDeadline returns a copy of b that also expires at deadline.
// Requests reserved after the deadline are refused, and the deadline
// is propagated to contexts via NewContext.
func (b *Budget) WithDeadline(deadline time.Time) *Budget {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &Budget{remaining: b.remaining, deadline: deadline, now: b.now}
}

// Remaining reports how many attempts can still be reserved.
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining
}

// Deadline reports the budget deadline, if any.
func (b *Budget) Deadline() (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deadline, !b.deadline.IsZero()
}

// Acquire reserves one attempt. It fails with ErrExhausted when the
// budget is empty, or ErrDeadlineExceeded once the deadline has passed.
// The returned Token must be either Spend (when the request is issued)
// or Released (to refund the reservation).
func (b *Budget) Acquire() (*Token, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deadlineExceededLocked() {
		return nil, ErrDeadlineExceeded
	}
	if b.remaining <= 0 {
		return nil, ErrExhausted
	}
	b.remaining--
	return &Token{b: b}, nil
}

func (b *Budget) deadlineExceededLocked() bool {
	return !b.deadline.IsZero() && !b.now().Before(b.deadline)
}

// refundLocked returns one attempt to the pool.
func (b *Budget) refund() {
	b.mu.Lock()
	b.remaining++
	b.mu.Unlock()
}

type tokenState int

const (
	reserved tokenState = iota
	spent
	released
)

// Token is a single reserved attempt. It is safe for concurrent use,
// but only the first of Spend/Release takes effect.
type Token struct {
	b     *Budget
	mu    sync.Mutex
	state tokenState
}

// Spend charges the token against the budget. It must be called only
// when the request is actually issued. It reports false if the token
// was already released (e.g. the attempt was cancelled), in which case
// the caller must not issue the request.
func (t *Token) Spend() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != reserved {
		return false
	}
	t.state = spent
	return true
}

// Release refunds an unspent reservation. It is a no-op on a token
// that was already spent or released.
func (t *Token) Release() {
	t.mu.Lock()
	if t.state != reserved {
		t.mu.Unlock()
		return
	}
	t.state = released
	t.mu.Unlock()
	t.b.refund()
}

// Spent reports whether the token has been charged.
func (t *Token) Spent() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == spent
}

// Reserved reports whether the token is still an unused reservation,
// i.e. it can still be spent or released.
func (t *Token) Reserved() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == reserved
}

type ctxKey struct{}
type tokenKey struct{}

// NewContext returns a context carrying b, plus a cancel function the
// caller must invoke when the operation completes. If b has a deadline,
// the returned context's deadline is the earlier of the parent's and
// the budget's, so cancellation propagates to every layer automatically.
func NewContext(ctx context.Context, b *Budget) (context.Context, context.CancelFunc) {
	ctx = context.WithValue(ctx, ctxKey{}, b)
	if d, ok := b.Deadline(); ok {
		// context.WithDeadline clamps to the earlier deadline.
		return context.WithDeadline(ctx, d)
	}
	return ctx, func() {}
}

// FromContext extracts the Budget attached with NewContext.
func FromContext(ctx context.Context) (*Budget, bool) {
	b, ok := ctx.Value(ctxKey{}).(*Budget)
	return b, ok
}

// tokenFromContext returns the reservation currently flowing through
// ctx, if any. Nested retry layers reuse it instead of reserving one
// token per layer, so a single logical attempt is never charged twice.
func tokenFromContext(ctx context.Context) *Token {
	t, _ := ctx.Value(tokenKey{}).(*Token)
	return t
}

// contextWithToken attaches a reservation for downstream layers.
func contextWithToken(ctx context.Context, t *Token) context.Context {
	return context.WithValue(ctx, tokenKey{}, t)
}

// IsBudgetError reports whether err was caused by budget exhaustion
// or the budget deadline, as opposed to a genuine request failure.
func IsBudgetError(err error) bool {
	return errors.Is(err, ErrExhausted) || errors.Is(err, ErrDeadlineExceeded)
}
