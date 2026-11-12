package netsec

import "time"

type config struct {
	clock func() time.Time
}

func defaultConfig() *config {
	return &config{clock: time.Now}
}

// Option customizes the barrier and its deletion log.
type Option func(*config)

// WithClock injects a clock used to timestamp deletions and rejection records.
// Production callers leave this unset; tests use it to get deterministic
// evidence.
func WithClock(now func() time.Time) Option {
	return func(c *config) {
		if now != nil {
			c.clock = now
		}
	}
}
