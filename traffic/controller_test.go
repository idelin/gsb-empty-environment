package traffic

import (
	"math/rand"
	"testing"
)

// flows: NS through (0), EW through (1), NS left (2), EW left (3).
// Conflicts: NS-through vs EW-through, NS-through vs EW-left,
// EW-through vs NS-left, NS-left vs EW-left.
var testFlows = []Flow{0, 1, 2, 3}

var testConflicts = [][2]Flow{
	{0, 1}, {0, 3}, {1, 2}, {2, 3},
}

func newTestController() *Controller {
	return NewController(3, 2, testFlows, testConflicts)
}

func requireSafe(t *testing.T, c *Controller) {
	t.Helper()
	if !c.Safe() {
		t.Fatalf("invariant violated at t=%d: lights=%v", c.Now(), snapshot(c))
	}
}

func snapshot(c *Controller) map[Flow]Light {
	m := make(map[Flow]Light, len(testFlows))
	for _, f := range testFlows {
		m[f] = c.Light(f)
	}
	return m
}

func TestSwitchPassesThroughYellowAndAllRed(t *testing.T) {
	c := newTestController()
	c.Request(0)
	c.Advance(2) // all-red from cold start
	if c.Light(0) != LightGreen {
		t.Fatalf("flow 0 should be green, got %v", c.Light(0))
	}

	c.Request(1)
	if c.Light(0) != LightYellow {
		t.Fatalf("flow 0 should be yellow, got %v", c.Light(0))
	}
	if c.Light(1) != LightRed {
		t.Fatalf("flow 1 must stay red during yellow, got %v", c.Light(1))
	}
	c.Advance(3) // yellow completes
	if c.Light(0) != LightRed {
		t.Fatalf("flow 0 should be red after yellow, got %v", c.Light(0))
	}
	if c.Light(1) != LightRed {
		t.Fatalf("all-red interval: flow 1 must still be red, got %v", c.Light(1))
	}
	c.Advance(2) // all-red completes
	if c.Light(1) != LightGreen {
		t.Fatalf("flow 1 should be green, got %v", c.Light(1))
	}
	requireSafe(t, c)
}

func TestNewRequestReplacesPendingTarget(t *testing.T) {
	c := newTestController()
	c.Request(0)
	c.Advance(2)
	c.Request(1) // start switching 0 -> 1
	c.Advance(1) // mid-yellow
	c.Request(2) // replace target before it starts
	c.Advance(4) // finish yellow + all-red
	if c.Light(2) != LightGreen {
		t.Fatalf("flow 2 should be green after retarget, got %v", c.Light(2))
	}
	if c.Light(1) != LightRed {
		t.Fatalf("flow 1 was replaced and must never turn green, got %v", c.Light(1))
	}
	requireSafe(t, c)
}

func TestStartedSafetyIntervalCannotBeShortened(t *testing.T) {
	c := newTestController()
	c.Request(0)
	c.Advance(2)

	c.Request(1)
	yellowStart := c.Now()
	c.Advance(2) // 1 tick of yellow remains
	c.Request(2) // retarget mid-yellow: must not change timing

	c.Advance(1) // yellow ends exactly 3 ticks after it started
	if got := c.Now(); got != yellowStart+3 {
		t.Fatalf("yellow interval was altered: now=%d, want %d", got, yellowStart+3)
	}
	if c.Light(0) != LightRed || c.Light(2) != LightRed {
		t.Fatalf("all-red must follow yellow, got %v", snapshot(c))
	}
	c.Advance(1) // mid all-red
	c.Request(3) // retarget during all-red: still cannot shorten
	if c.Light(3) != LightRed {
		t.Fatalf("flow 3 must wait out the all-red interval, got %v", c.Light(3))
	}
	c.Advance(1) // all-red ends exactly 2 ticks after it started
	if c.Light(3) != LightGreen {
		t.Fatalf("flow 3 should be green after full all-red, got %v", c.Light(3))
	}
	if got := c.Now(); got != yellowStart+5 {
		t.Fatalf("total safety interval was shortened: now=%d, want %d", got, yellowStart+5)
	}
	requireSafe(t, c)
}

func TestCancelRevertsToOriginFlow(t *testing.T) {
	c := newTestController()
	c.Request(0)
	c.Advance(2)
	c.Request(1)
	c.Advance(1) // mid-yellow
	c.Cancel()
	c.Advance(4) // yellow + all-red still run in full
	if c.Light(0) != LightGreen {
		t.Fatalf("cancel should restore origin flow 0 to green, got %v", c.Light(0))
	}
	if c.Light(1) != LightRed {
		t.Fatalf("cancelled flow 1 must stay red, got %v", c.Light(1))
	}
	requireSafe(t, c)
}

func TestCancelDuringAllRed(t *testing.T) {
	c := newTestController()
	c.Request(0)
	c.Advance(2)
	c.Request(1)
	c.Advance(4) // yellow done, mid all-red
	c.Cancel()
	c.Advance(1)
	if c.Light(0) != LightGreen {
		t.Fatalf("cancel during all-red should restore flow 0, got %v", c.Light(0))
	}
	requireSafe(t, c)
}

func TestConsecutiveRequestsAndInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		c := newTestController()
		for step := 0; step < 500; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5: // request a random flow
				c.Request(testFlows[rng.Intn(len(testFlows))])
			case 6: // cancel pending target
				c.Cancel()
			default: // advance the virtual clock 0..4 ticks
				c.Advance(int64(rng.Intn(5)))
			}
			requireSafe(t, c)
		}
	}
}

func TestBackToBackRequestsWithoutClockAdvance(t *testing.T) {
	c := newTestController()
	c.Request(0)
	c.Advance(2)
	// Hammer consecutive requests with no time passing in between.
	for _, f := range []Flow{1, 2, 3, 1, 2} {
		c.Request(f)
		requireSafe(t, c)
	}
	c.Advance(5)
	if c.Light(2) != LightGreen {
		t.Fatalf("last request should win, got %v", snapshot(c))
	}
	requireSafe(t, c)
}
