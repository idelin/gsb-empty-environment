package traffic

import (
	"math/rand"
	"testing"
	"time"
)

const (
	testYellow = 3 * time.Second
	testAllRed = 2 * time.Second
	tick       = 50 * time.Millisecond
)

func newTestController(t *testing.T, flows ...string) *Controller {
	t.Helper()
	c, err := New(flows, testYellow, testAllRed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// step advances the virtual clock in small ticks, checking the conflict
// invariant at every instant.
func step(t *testing.T, c *Controller, d time.Duration) {
	t.Helper()
	for d > 0 {
		s := tick
		if s > d {
			s = d
		}
		c.Advance(s)
		if err := c.CheckInvariant(); err != nil {
			t.Fatalf("invariant violated: %v", err)
		}
		d -= s
	}
}

func mustRequest(t *testing.T, c *Controller, flow string) {
	t.Helper()
	if err := c.Request(flow); err != nil {
		t.Fatalf("Request(%q): %v", flow, err)
	}
}

func allRed(c *Controller, flows ...string) bool {
	for _, f := range flows {
		if c.State(f) != Red {
			return false
		}
	}
	return true
}

func TestGreenSwitchPassesThroughYellowAndAllRed(t *testing.T) {
	c := newTestController(t, "north", "east")
	mustRequest(t, c, "north")
	if got := c.State("north"); got != Green {
		t.Fatalf("first request from all-red hold: north = %v, want green", got)
	}
	step(t, c, 5*time.Second)

	mustRequest(t, c, "east")
	if got := c.State("north"); got != Yellow {
		t.Fatalf("after request: north = %v, want yellow", got)
	}
	reqAt := c.Now()

	step(t, c, testYellow-tick)
	if got := c.State("north"); got != Yellow {
		t.Fatalf("during yellow interval: north = %v, want yellow", got)
	}
	step(t, c, tick) // yellow ends exactly here
	if !allRed(c, "north", "east") {
		t.Fatalf("after yellow: want all red, got north=%v east=%v",
			c.State("north"), c.State("east"))
	}
	step(t, c, testAllRed-tick)
	if !allRed(c, "north", "east") {
		t.Fatalf("during all-red: want all red, got north=%v east=%v",
			c.State("north"), c.State("east"))
	}
	step(t, c, tick) // all-red ends exactly here
	if got := c.State("east"); got != Green {
		t.Fatalf("after all-red: east = %v, want green", got)
	}
	if want := reqAt + testYellow + testAllRed; c.Now() != want {
		t.Fatalf("green started at %v, want exactly %v after request", c.Now(), want)
	}
}

func TestNewRequestReplacesUnstartedTarget(t *testing.T) {
	c := newTestController(t, "north", "east", "south")
	mustRequest(t, c, "north")
	step(t, c, 5*time.Second)

	mustRequest(t, c, "east")
	reqAt := c.Now()
	step(t, c, time.Second) // mid-yellow
	mustRequest(t, c, "south")

	step(t, c, reqAt+testYellow+testAllRed-c.Now())
	if got := c.State("south"); got != Green {
		t.Fatalf("south = %v, want green", got)
	}
	if got := c.State("east"); got != Red {
		t.Fatalf("replaced target east = %v, want red (never started)", got)
	}
	if want := reqAt + testYellow + testAllRed; c.Now() != want {
		t.Fatalf("replacement changed switch timing: green at %v, want %v", c.Now(), want)
	}
}

func TestStartedSafetyIntervalCannotBeShortened(t *testing.T) {
	c := newTestController(t, "north", "east", "south")
	mustRequest(t, c, "north")
	step(t, c, 5*time.Second)

	mustRequest(t, c, "east")
	reqAt := c.Now()
	step(t, c, testYellow+time.Second) // inside all-red now
	if !allRed(c, "north", "east", "south") {
		t.Fatal("expected all-red in progress")
	}
	mustRequest(t, c, "south")

	step(t, c, reqAt+testYellow+testAllRed-c.Now())
	if got := c.State("south"); got != Green {
		t.Fatalf("south = %v, want green", got)
	}
	if want := reqAt + testYellow + testAllRed; c.Now() != want {
		t.Fatalf("all-red interval was altered: green at %v, want %v", c.Now(), want)
	}
}

func TestCancelPendingTargetHoldsAllRed(t *testing.T) {
	c := newTestController(t, "north", "east", "south")
	mustRequest(t, c, "north")
	step(t, c, 5*time.Second)

	mustRequest(t, c, "east")
	step(t, c, time.Second) // mid-yellow
	c.Cancel("east")

	step(t, c, 10*time.Second)
	if !allRed(c, "north", "east", "south") {
		t.Fatalf("after cancel: want all-red hold, got north=%v east=%v south=%v",
			c.State("north"), c.State("east"), c.State("south"))
	}

	mustRequest(t, c, "south")
	if got := c.State("south"); got != Green {
		t.Fatalf("request from all-red hold: south = %v, want green", got)
	}
}

func TestConsecutiveRequestsCauseSingleTransitionCycle(t *testing.T) {
	c := newTestController(t, "north", "east", "south", "west")
	mustRequest(t, c, "north")
	step(t, c, 5*time.Second)

	mustRequest(t, c, "east")
	reqAt := c.Now()
	step(t, c, 500*time.Millisecond)
	mustRequest(t, c, "south")
	step(t, c, 500*time.Millisecond)
	mustRequest(t, c, "west")

	greenAt := time.Duration(-1)
	for c.Now() < reqAt+testYellow+testAllRed+5*time.Second {
		step(t, c, tick)
		if c.State("west") == Green && greenAt < 0 {
			greenAt = c.Now()
		}
		if c.State("east") == Green || c.State("south") == Green {
			t.Fatalf("replaced target turned green at %v", c.Now())
		}
	}
	if want := reqAt + testYellow + testAllRed; greenAt != want {
		t.Fatalf("west green at %v, want single cycle ending %v", greenAt, want)
	}
}

func TestRequestDepartingFlowReturnsToGreen(t *testing.T) {
	c := newTestController(t, "north", "east")
	mustRequest(t, c, "north")
	step(t, c, 5*time.Second)

	mustRequest(t, c, "east")
	reqAt := c.Now()
	step(t, c, time.Second)
	mustRequest(t, c, "north")

	step(t, c, reqAt+testYellow+testAllRed-c.Now())
	if got := c.State("north"); got != Green {
		t.Fatalf("north = %v, want green again", got)
	}
	if got := c.State("east"); got != Red {
		t.Fatalf("east = %v, want red (never started)", got)
	}
}

func TestUnknownFlowRejected(t *testing.T) {
	c := newTestController(t, "north", "east")
	if err := c.Request("south"); err == nil {
		t.Fatal("Request(unknown): want error")
	}
	if _, err := New([]string{"only"}, testYellow, testAllRed); err == nil {
		t.Fatal("New with one flow: want error")
	}
	if _, err := New([]string{"a", "a"}, testYellow, testAllRed); err == nil {
		t.Fatal("New with duplicate flow: want error")
	}
}

// TestRandomizedInvariant hammers the controller with deterministic random
// requests, cancels and clock advances. At every tick it checks the conflict
// invariant plus the timing rules: a green only starts after a full yellow
// plus all-red sequence, and yellow lasts exactly the configured interval.
func TestRandomizedInvariant(t *testing.T) {
	flows := []string{"a", "b", "c", "d"}
	c := newTestController(t, flows...)
	rng := rand.New(rand.NewSource(42))

	prev := map[string]State{}
	yellowSince := map[string]time.Duration{}
	redSince := time.Duration(-1)
	greenSeen := false

	checkTransitions := func() {
		anyNonRed := false
		for _, f := range flows {
			now, was := c.State(f), prev[f]
			if now != Red {
				anyNonRed = true
			}
			switch {
			case now == Green && was == Red:
				if redSince < 0 {
					t.Fatalf("%v: %q turned green without a preceding all-red", c.Now(), f)
				}
				if got := c.Now() - redSince; got < testAllRed {
					t.Fatalf("%v: %q green after only %v all-red, want >= %v", c.Now(), f, got, testAllRed)
				}
				greenSeen = true
			case now == Green && was == Yellow:
				t.Fatalf("%v: %q jumped yellow->green, skipping all-red", c.Now(), f)
			case now == Yellow && was == Green:
				yellowSince[f] = c.Now()
			case now == Yellow && was != Yellow:
				t.Fatalf("%v: %q became yellow from %v, want from green", c.Now(), f, was)
			case now == Red && was == Yellow:
				if got := c.Now() - yellowSince[f]; got != testYellow {
					t.Fatalf("%v: %q yellow lasted %v, want exactly %v", c.Now(), f, got, testYellow)
				}
			}
			prev[f] = now
		}
		if !anyNonRed && redSince < 0 {
			redSince = c.Now()
		} else if anyNonRed {
			redSince = -1
		}
	}

	for _, f := range flows {
		prev[f] = c.State(f)
	}
	redSince = 0 // boot state is an all-red hold

	advance := func(d time.Duration) {
		for d > 0 {
			c.Advance(tick)
			if err := c.CheckInvariant(); err != nil {
				t.Fatalf("invariant violated: %v", err)
			}
			checkTransitions()
			d -= tick
		}
	}

	for i := 0; i < 3000; i++ {
		switch rng.Intn(3) {
		case 0:
			mustRequest(t, c, flows[rng.Intn(len(flows))])
		case 1:
			c.Cancel(flows[rng.Intn(len(flows))])
		}
		checkTransitions() // catch transitions applied synchronously by the op
		advance(time.Duration(1+rng.Intn(40)) * tick)
	}
	if !greenSeen {
		t.Fatal("randomized run never reached green; test is vacuous")
	}
}
