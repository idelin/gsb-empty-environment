package router

// Clock supplies the current tick. The router never reads a wall clock.
type Clock interface {
	Now() uint64
}

// ManualClock is a test clock advanced explicitly by the caller.
type ManualClock struct {
	now uint64
}

// NewManualClock returns a ManualClock starting at tick 0.
func NewManualClock() *ManualClock { return &ManualClock{} }

// Now returns the current tick.
func (c *ManualClock) Now() uint64 { return c.now }

// Advance moves the clock forward by n ticks.
func (c *ManualClock) Advance(n uint64) { c.now += n }
