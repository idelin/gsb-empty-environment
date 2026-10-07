// Package traffic implements a virtual-clock signal controller for a set of
// mutually conflicting traffic flows. A green switch always passes through
// the departing flow's yellow interval followed by an all-red interval.
// A new request may replace a target whose green has not started yet, but
// never shortens a safety interval (yellow / all-red) already in progress.
package traffic

import (
	"fmt"
	"time"
)

// State is the signal aspect of a single flow.
type State int

const (
	Red State = iota
	Yellow
	Green
)

func (s State) String() string {
	switch s {
	case Red:
		return "red"
	case Yellow:
		return "yellow"
	case Green:
		return "green"
	}
	return "unknown"
}

type phase int

const (
	phaseGreen phase = iota
	phaseYellow
	phaseAllRed
)

// Controller drives one group of mutually conflicting flows on a virtual
// clock. It is not safe for concurrent use; time advances only via Advance.
type Controller struct {
	flows  []string
	states map[string]State
	yellow time.Duration
	allRed time.Duration

	now       time.Duration
	phase     phase
	current   string // flow holding green, or departing it while yellow
	target    string
	hasTarget bool
	phaseEnd  time.Duration
	// allRedSatisfied reports whether the minimum all-red interval has
	// elapsed; the controller may hold in all-red afterwards.
	allRedSatisfied bool
}

// New builds a controller for the given conflicting flows. All flows start
// red with the minimum all-red interval considered already elapsed, so the
// first request turns green immediately.
func New(flows []string, yellow, allRed time.Duration) (*Controller, error) {
	if len(flows) < 2 {
		return nil, fmt.Errorf("traffic: need at least 2 conflicting flows, got %d", len(flows))
	}
	if yellow <= 0 || allRed <= 0 {
		return nil, fmt.Errorf("traffic: yellow and all-red intervals must be positive")
	}
	c := &Controller{
		states:          make(map[string]State, len(flows)),
		yellow:          yellow,
		allRed:          allRed,
		phase:           phaseAllRed,
		allRedSatisfied: true,
	}
	seen := make(map[string]bool, len(flows))
	for _, f := range flows {
		if f == "" {
			return nil, fmt.Errorf("traffic: empty flow name")
		}
		if seen[f] {
			return nil, fmt.Errorf("traffic: duplicate flow %q", f)
		}
		seen[f] = true
		c.flows = append(c.flows, f)
		c.states[f] = Red
	}
	return c, nil
}

// Now returns the current virtual time.
func (c *Controller) Now() time.Duration { return c.now }

// State returns the current aspect of a flow.
func (c *Controller) State(flow string) State { return c.states[flow] }

// Request asks for flow to become green. From green it starts the yellow
// interval immediately. During yellow or all-red it replaces the pending
// (not yet started) target without altering the running safety intervals.
// Requesting the departing flow itself makes the transition return to it.
func (c *Controller) Request(flow string) error {
	if _, ok := c.states[flow]; !ok {
		return fmt.Errorf("traffic: unknown flow %q", flow)
	}
	switch c.phase {
	case phaseGreen:
		if flow == c.current {
			return nil
		}
		c.states[c.current] = Yellow
		c.phase = phaseYellow
		c.phaseEnd = c.now + c.yellow
		c.target = flow
		c.hasTarget = true
	case phaseYellow:
		c.target = flow
		c.hasTarget = true
	case phaseAllRed:
		if c.allRedSatisfied {
			c.states[flow] = Green
			c.current = flow
			c.phase = phaseGreen
			return nil
		}
		c.target = flow
		c.hasTarget = true
	}
	return nil
}

// Cancel drops a pending target that has not started. If the cancelled flow
// is the target of a running transition, the controller finishes the safety
// intervals and then holds in all-red until the next request.
func (c *Controller) Cancel(flow string) {
	if c.hasTarget && c.target == flow {
		c.target = ""
		c.hasTarget = false
	}
}

// Advance moves the virtual clock forward, applying every phase boundary
// crossed along the way.
func (c *Controller) Advance(d time.Duration) {
	if d < 0 {
		panic("traffic: cannot advance by a negative duration")
	}
	end := c.now + d
	for c.now < end {
		switch c.phase {
		case phaseGreen:
			c.now = end
		case phaseYellow:
			if c.phaseEnd > end {
				c.now = end
				continue
			}
			c.now = c.phaseEnd
			c.states[c.current] = Red
			c.phase = phaseAllRed
			c.allRedSatisfied = false
			c.phaseEnd = c.now + c.allRed
		case phaseAllRed:
			if c.allRedSatisfied {
				c.now = end
				continue
			}
			if c.phaseEnd > end {
				c.now = end
				continue
			}
			c.now = c.phaseEnd
			c.allRedSatisfied = true
			if c.hasTarget {
				c.states[c.target] = Green
				c.current = c.target
				c.target = ""
				c.hasTarget = false
				c.phase = phaseGreen
			}
		}
	}
}

// CheckInvariant verifies that no two conflicting flows are green (or one
// green while another is yellow) at the current instant.
func (c *Controller) CheckInvariant() error {
	var green, yellow []string
	for _, f := range c.flows {
		switch c.states[f] {
		case Green:
			green = append(green, f)
		case Yellow:
			yellow = append(yellow, f)
		}
	}
	if len(green) > 1 {
		return fmt.Errorf("traffic: conflicting flows green at %v: %v", c.now, green)
	}
	if len(green) > 0 && len(yellow) > 0 {
		return fmt.Errorf("traffic: green %v overlaps yellow %v at %v", green, yellow, c.now)
	}
	if len(yellow) > 1 {
		return fmt.Errorf("traffic: multiple yellow flows at %v: %v", c.now, yellow)
	}
	return nil
}
