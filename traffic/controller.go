// Package traffic implements a virtual-clock-driven traffic light state
// machine. Flows are grouped into conflict sets; switching the green light
// between conflicting flows always passes through a yellow phase for the
// currently served flow followed by an all-red clearance interval.
//
// Safety rules:
//   - A green switch must complete the full yellow and all-red intervals.
//   - A new request may replace a target that has not started (its green
//     has not begun), but it never shortens an already-started safety
//     interval: the scheduled end of a running yellow/all-red phase is
//     immutable.
//   - Cancelling a pending target reverts the transition to the origin
//     flow, again without altering the running safety intervals.
//
// Invariant: at any virtual time, no two conflicting flows are green.
package traffic

// Flow identifies a traffic movement (e.g. a through lane or turn lane).
type Flow int

// Light is the signal aspect of a flow.
type Light int

const (
	LightRed Light = iota
	LightYellow
	LightGreen
)

func (l Light) String() string {
	switch l {
	case LightRed:
		return "red"
	case LightYellow:
		return "yellow"
	case LightGreen:
		return "green"
	}
	return "unknown"
}

type stage int

const (
	stageYellow stage = iota
	stageAllRed
)

// Controller is the traffic light state machine. Time is virtual: it only
// advances when Advance is called, in integer ticks.
type Controller struct {
	yellowTicks int64
	allRedTicks int64

	conflicts map[Flow]map[Flow]bool
	lights    map[Flow]Light
	now       int64

	active    Flow // flow currently holding green
	hasActive bool

	transitioning bool
	origin        Flow  // flow that held green when the transition began
	target        Flow  // flow scheduled to receive green next
	stage         stage // current safety interval
	stageEnd      int64 // virtual tick at which the current interval ends
}

// NewController builds a controller where every flow starts red.
// yellowTicks and allRedTicks are the fixed safety interval lengths and
// must be positive. conflictPairs lists unordered pairs of flows that may
// never be green simultaneously.
func NewController(yellowTicks, allRedTicks int64, flows []Flow, conflictPairs [][2]Flow) *Controller {
	if yellowTicks <= 0 || allRedTicks <= 0 {
		panic("safety intervals must be positive")
	}
	c := &Controller{
		yellowTicks: yellowTicks,
		allRedTicks: allRedTicks,
		conflicts:   make(map[Flow]map[Flow]bool),
		lights:      make(map[Flow]Light, len(flows)),
	}
	for _, f := range flows {
		c.lights[f] = LightRed
	}
	for _, p := range conflictPairs {
		a, b := p[0], p[1]
		if c.conflicts[a] == nil {
			c.conflicts[a] = make(map[Flow]bool)
		}
		if c.conflicts[b] == nil {
			c.conflicts[b] = make(map[Flow]bool)
		}
		c.conflicts[a][b] = true
		c.conflicts[b][a] = true
	}
	return c
}

// Now returns the current virtual tick.
func (c *Controller) Now() int64 { return c.now }

// Light returns the current aspect of a flow.
func (c *Controller) Light(f Flow) Light { return c.lights[f] }

// Transitioning reports whether a green switch is in progress.
func (c *Controller) Transitioning() bool { return c.transitioning }

// Request asks for flow f to receive green. If no transition is running it
// starts one (unless f already holds green). If a transition is running,
// the not-yet-started target is replaced by f; requesting the origin flow
// is equivalent to cancelling. A running safety interval is never
// shortened: its scheduled end tick is left untouched.
func (c *Controller) Request(f Flow) {
	if _, ok := c.lights[f]; !ok {
		panic("unknown flow")
	}
	if c.transitioning {
		c.target = f
		return
	}
	if c.hasActive && c.active == f {
		return
	}
	if !c.hasActive {
		// Nothing is green: no yellow needed, but the all-red clearance
		// interval still applies before granting green.
		c.origin = f
		c.target = f
		c.stage = stageAllRed
		c.stageEnd = c.now + c.allRedTicks
		c.transitioning = true
		return
	}
	c.origin = c.active
	c.target = f
	c.lights[c.active] = LightYellow
	c.stage = stageYellow
	c.stageEnd = c.now + c.yellowTicks
	c.transitioning = true
}

// Cancel withdraws a pending target, reverting the transition to the flow
// that held green when it started. The running safety intervals still
// complete in full. It is a no-op when no transition is in progress.
func (c *Controller) Cancel() {
	if c.transitioning {
		c.target = c.origin
	}
}

// Advance moves the virtual clock forward by d ticks, completing any
// safety intervals that expire along the way, in order.
func (c *Controller) Advance(d int64) {
	if d < 0 {
		panic("cannot move the clock backwards")
	}
	end := c.now + d
	for c.transitioning && c.stageEnd <= end {
		c.now = c.stageEnd
		c.completeStage()
	}
	c.now = end
}

func (c *Controller) completeStage() {
	switch c.stage {
	case stageYellow:
		c.lights[c.origin] = LightRed
		c.stage = stageAllRed
		c.stageEnd = c.now + c.allRedTicks
	case stageAllRed:
		c.lights[c.target] = LightGreen
		c.active = c.target
		c.hasActive = true
		c.transitioning = false
	}
}

// Safe verifies the safety invariant: no two conflicting flows are green
// at the same time. It should hold after every exported method call.
func (c *Controller) Safe() bool {
	for f, light := range c.lights {
		if light != LightGreen {
			continue
		}
		for other := range c.conflicts[f] {
			if c.lights[other] == LightGreen {
				return false
			}
		}
	}
	return true
}
