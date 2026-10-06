package thermosim

// Clock is a manual virtual clock for deterministic simulation and
// testing. It only moves forward.
type Clock struct {
	now int64
}

// NewClock creates a virtual clock starting at the given time.
func NewClock(start int64) *Clock { return &Clock{now: start} }

// Now returns the current virtual time.
func (c *Clock) Now() int64 { return c.now }

// AdvanceTo moves the clock to t; it panics if t is in the past.
func (c *Clock) AdvanceTo(t int64) {
	if t < c.now {
		panic("thermosim: virtual clock cannot go backwards")
	}
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d int64) { c.AdvanceTo(c.now + d) }
