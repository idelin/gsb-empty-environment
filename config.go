package netsec

import "time"

const (
	defaultRebindDelay = 24 * time.Hour
	defaultMaxAmount   = int64(10_000_000_000) // 100,000,000.00 in minor units
	defaultMaxRecords  = 100_000
)

type config struct {
	clock             func() time.Time
	rebindDelay       time.Duration
	minAmountCents    int64
	maxAmountCents    int64
	maxIdempotencyLen int
}

// Option customizes an Attributor. The defaults are safe for production;
// options mainly exist for embedding, testing, and institution-specific
// limits.
type Option func(*config)

// WithClock injects the clock used for approval and effective times. It is
// primarily useful for deterministic tests. A nil clock is ignored.
func WithClock(clock func() time.Time) Option {
	return func(c *config) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// WithRebindDelay sets the delay between approval and activation of a terminal
// rebind. A non-positive value is ignored so callers cannot accidentally
// remove the review delay.
func WithRebindDelay(delay time.Duration) Option {
	return func(c *config) {
		if delay > 0 {
			c.rebindDelay = delay
		}
	}
}

// WithAmountLimits sets the global per-settlement limits in minor units.
// Non-positive values are ignored.
func WithAmountLimits(minCents, maxCents int64) Option {
	return func(c *config) {
		if minCents > 0 {
			c.minAmountCents = minCents
		}
		if maxCents > 0 {
			c.maxAmountCents = maxCents
		}
	}
}

// WithMaxAmount is a convenience option for setting only the upper limit.
func WithMaxAmount(maxCents int64) Option {
	return WithAmountLimits(0, maxCents)
}

// WithMaxIdempotencyRecords bounds the replay cache. Non-positive values are
// ignored.
func WithMaxIdempotencyRecords(max int) Option {
	return func(c *config) {
		if max > 0 {
			c.maxIdempotencyLen = max
		}
	}
}

func newConfig(options []Option) config {
	c := config{
		clock:             time.Now,
		rebindDelay:       defaultRebindDelay,
		minAmountCents:    1,
		maxAmountCents:    defaultMaxAmount,
		maxIdempotencyLen: defaultMaxRecords,
	}
	for _, option := range options {
		if option != nil {
			option(&c)
		}
	}
	if c.minAmountCents > c.maxAmountCents {
		c.minAmountCents = c.maxAmountCents
	}
	return c
}
