package retry

import (
	"math"
	"sync"
	"time"
)

// RetryBudgetConfig configures the sliding-window retry budget of one edge.
//
// AllowedRetries = max(MinRetryConcurrency, floor(BudgetRatio * activeNormal))
//
// A negative BudgetRatio (or +Inf) disables the budget: every retry-class
// attempt is admitted. This models the legacy "retry storm" configuration
// used in the avalanche acceptance test.
type RetryBudgetConfig struct {
	BudgetRatio         float64
	MinRetryConcurrency int
	Window              time.Duration
}

// RetryBudget tracks normal requests in a sliding window, outstanding normal
// attempts and outstanding retry-class attempts (retries and hedged
// branches both consume budget slots).
type RetryBudget struct {
	cfg    RetryBudgetConfig
	window *SlidingWindow

	mu            sync.Mutex
	activeNormal  int
	retryInFlight int
}

// NewRetryBudget builds a budget from cfg.
func NewRetryBudget(cfg RetryBudgetConfig) *RetryBudget {
	if cfg.Window <= 0 {
		cfg.Window = 10 * time.Second
	}
	return &RetryBudget{
		cfg:    cfg,
		window: NewSlidingWindow(cfg.Window),
	}
}

// Unlimited reports whether any retry-class attempt is admitted.
func (b *RetryBudget) Unlimited() bool {
	return b.cfg.BudgetRatio < 0 || math.IsInf(b.cfg.BudgetRatio, 1)
}

// NormalStarted must be called when a (non-retry) attempt begins. It feeds
// both the sliding window and the outstanding-normal counter.
func (b *RetryBudget) NormalStarted(now time.Time) {
	b.mu.Lock()
	b.activeNormal++
	b.mu.Unlock()
	b.window.Add(now)
}

// NormalEnded must be called when a normal attempt finishes in any state.
func (b *RetryBudget) NormalEnded() {
	b.mu.Lock()
	if b.activeNormal > 0 {
		b.activeNormal--
	}
	b.mu.Unlock()
}

// AllowedRetries computes the current retry-class concurrency limit.
func (b *RetryBudget) AllowedRetries(now time.Time) int {
	if b.Unlimited() {
		return math.MaxInt
	}
	b.mu.Lock()
	active := b.activeNormal
	b.mu.Unlock()
	ratioSlots := int(math.Floor(b.cfg.BudgetRatio * float64(active)))
	if ratioSlots < b.cfg.MinRetryConcurrency {
		return b.cfg.MinRetryConcurrency
	}
	return ratioSlots
}

// TryAcquire consumes one retry-class slot if the budget permits.
func (b *RetryBudget) TryAcquire(now time.Time) bool {
	if b.Unlimited() {
		b.mu.Lock()
		b.retryInFlight++
		b.mu.Unlock()
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ratioSlots := int(math.Floor(b.cfg.BudgetRatio * float64(b.activeNormal)))
	allowed := ratioSlots
	if allowed < b.cfg.MinRetryConcurrency {
		allowed = b.cfg.MinRetryConcurrency
	}
	if b.retryInFlight >= allowed {
		return false
	}
	b.retryInFlight++
	return true
}

// Release returns one retry-class slot (attempt finished or was cancelled).
func (b *RetryBudget) Release() {
	b.mu.Lock()
	if b.retryInFlight > 0 {
		b.retryInFlight--
	}
	b.mu.Unlock()
}

// RetryInFlight reports outstanding retry-class attempts.
func (b *RetryBudget) RetryInFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.retryInFlight
}

// ActiveNormal reports outstanding normal attempts.
func (b *RetryBudget) ActiveNormal() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.activeNormal
}

// WindowCount reports normal request starts still inside the rolling window.
func (b *RetryBudget) WindowCount(now time.Time) int {
	return b.window.Count(now)
}

// Config returns the budget configuration.
func (b *RetryBudget) Config() RetryBudgetConfig { return b.cfg }
