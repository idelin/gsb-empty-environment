package barrier

import "fmt"

// TestClock is a monotonic logical clock. It only moves forward;
// rewinding panics so replayed schedules stay deterministic.
type TestClock struct {
	now Tick
}

func NewTestClock() *TestClock { return &TestClock{} }

func (c *TestClock) Now() Tick { return c.now }

// Advance moves the clock to `to`. It panics on a backwards move.
func (c *TestClock) Advance(to Tick) {
	if to < c.now {
		panic(fmt.Sprintf("barrier: non-monotonic clock advance %d -> %d", c.now, to))
	}
	c.now = to
}
