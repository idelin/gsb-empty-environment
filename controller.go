// Package thermosim simulates thermostat-style switch controllers driven by
// explicit temperature samples, plus a global arbiter that caps how many
// controllers may be On at once.
//
// The simulator is sample-driven: time only advances when a controller
// processes an explicit Sample. It never interpolates or synthesizes
// samples, so every switch happens exactly at a sample timestamp.
package thermosim

import (
	"errors"
	"fmt"
	"time"
)

// ErrLateSample is returned when a sample's timestamp is not strictly after
// the last processed sample. Late or duplicate samples are rejected and have
// no effect on the controller.
var ErrLateSample = errors.New("thermosim: late or duplicate sample rejected")

// State is the switch position of a Controller.
type State int

const (
	Off State = iota
	On
)

func (s State) String() string {
	if s == On {
		return "On"
	}
	return "Off"
}

// Sample is one explicit temperature observation.
type Sample struct {
	Time time.Time
	Temp float64
}

// Event records a single switch transition for timeline verification.
type Event struct {
	Time       time.Time
	Controller string
	From, To   State
	Reason     string
}

// Reasons recorded on Events.
const (
	ReasonBelowLower = "temp <= lower threshold"
	ReasonAboveUpper = "temp >= upper threshold"
	ReasonGrant      = "arbiter grant"
	ReasonGrantWait  = "arbiter grant after wait"
)

// Controller is a hysteresis thermostat with a minimum dwell time.
//
// Switching policy, evaluated once per sample:
//   - temp >= Upper -> Off (checked first, so equal thresholds resolve to Off)
//   - temp <= Lower -> On
//   - otherwise     -> keep the current state (and any pending request)
//
// After every switch the controller must dwell for MinDwell before it may
// switch again. If a sample demands a switch while the dwell is unmet, the
// decision is deferred and re-evaluated at the next sample; the controller
// never interpolates a switch between samples.
type Controller struct {
	Name     string
	Upper    float64
	Lower    float64
	MinDwell time.Duration
	// Priority is used by the Arbiter when capacity is scarce; smaller wins.
	Priority int
	// Arbiter, if non-nil, must approve every Off -> On transition.
	Arbiter *Arbiter

	// OnEvent, if set, is called on every switch.
	OnEvent func(Event)

	state      State
	switched   bool
	lastSwitch time.Time
	hasSample  bool
	lastSample time.Time
	pending    bool
}

// State reports the current switch position.
func (c *Controller) State() State { return c.state }

// Pending reports whether the controller is waiting for an arbiter grant.
func (c *Controller) Pending() bool { return c.pending }

// LastSwitch reports the timestamp of the most recent switch.
func (c *Controller) LastSwitch() (time.Time, bool) { return c.lastSwitch, c.switched }

// Process feeds one explicit temperature sample to the controller.
//
// Samples must arrive with strictly increasing timestamps; anything else is
// rejected with ErrLateSample and leaves the controller untouched.
func (c *Controller) Process(s Sample) error {
	if c.hasSample && !s.Time.After(c.lastSample) {
		return fmt.Errorf("%w: controller %q last sample at %s, got %s",
			ErrLateSample, c.Name,
			c.lastSample.Format(time.RFC3339), s.Time.Format(time.RFC3339))
	}
	c.hasSample = true
	c.lastSample = s.Time

	want, demanded := c.demand(s.Temp)
	if !demanded {
		return nil // hysteresis band: hold state and any pending request
	}

	if want == On {
		if c.state == On || c.pending {
			return nil // already on, or already waiting for a grant
		}
		if !c.dwellSatisfied(s.Time) {
			return nil // dwell unmet: defer the decision to the next sample
		}
		if c.Arbiter == nil {
			c.switchTo(On, s.Time, ReasonBelowLower)
			return nil
		}
		c.Arbiter.request(c, s.Time)
		return nil
	}

	// want == Off
	if c.pending {
		c.Arbiter.cancel(c)
	}
	if c.state == Off {
		return nil
	}
	if !c.dwellSatisfied(s.Time) {
		return nil // dwell may not be interrupted, even to switch off
	}
	c.switchTo(Off, s.Time, ReasonAboveUpper)
	if c.Arbiter != nil {
		c.Arbiter.release(c, s.Time)
	}
	return nil
}

// demand maps a temperature to a target state. The upper threshold is
// checked first, so with Upper == Lower a temperature exactly on the
// threshold resolves to Off.
func (c *Controller) demand(temp float64) (State, bool) {
	if temp >= c.Upper {
		return Off, true
	}
	if temp <= c.Lower {
		return On, true
	}
	return c.state, false
}

func (c *Controller) dwellSatisfied(t time.Time) bool {
	if !c.switched {
		return true
	}
	return !t.Before(c.lastSwitch.Add(c.MinDwell))
}

func (c *Controller) switchTo(next State, t time.Time, reason string) {
	if c.state == next {
		return
	}
	prev := c.state
	c.state = next
	c.switched = true
	c.lastSwitch = t
	if c.OnEvent != nil {
		c.OnEvent(Event{Time: t, Controller: c.Name, From: prev, To: next, Reason: reason})
	}
}
