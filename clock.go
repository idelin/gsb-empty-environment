package main

// Clock is a purely simulated clock. It never reads the wall clock;
// only the simulator may advance it via Advance.
type Clock struct {
	now int64
}

func NewClock() *Clock { return &Clock{} }

func (c *Clock) Now() int64 { return c.now }

// Advance moves the simulated clock forward. It is the only way time
// passes in the system, and it is monotonic by construction.
func (c *Clock) Advance(delta int64) {
	if delta < 0 {
		panic("clock cannot move backwards")
	}
	c.now += delta
}
