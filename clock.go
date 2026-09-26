package tailsampler

import "time"

// Clock abstracts time so tests and fixture replays can drive it manually.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManualClock is a deterministic clock for tests and replays.
type ManualClock struct {
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock { return &ManualClock{now: start} }

func (c *ManualClock) Now() time.Time { return c.now }

func (c *ManualClock) Set(t time.Time) { c.now = t }

func (c *ManualClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
