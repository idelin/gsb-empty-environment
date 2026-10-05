package sessionrouter

// Clock supplies logical time to the router.
type Clock interface {
	Now() Tick
}

// TestClock is a manually advanced Clock for deterministic tests.
type TestClock struct {
	tick Tick
}

func NewTestClock() *TestClock { return &TestClock{} }

func (c *TestClock) Now() Tick { return c.tick }

// AdvanceTo moves the clock forward; it never moves it backward.
func (c *TestClock) AdvanceTo(t Tick) {
	if t > c.tick {
		c.tick = t
	}
}

// Advance moves the clock forward by d ticks.
func (c *TestClock) Advance(d int64) { c.tick += Tick(d) }
