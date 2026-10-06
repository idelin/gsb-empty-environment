package main

// Clock is a purely logical clock. Only the simulator may advance it;
// leases and the resource never read a wall clock.
type Clock struct {
	now int64
}

func (c *Clock) Now() int64 { return c.now }

// Advance moves time forward. Monotonic by construction: d must be > 0.
func (c *Clock) Advance(d int64) {
	if d <= 0 {
		panic("clock only moves forward")
	}
	c.now += d
}
