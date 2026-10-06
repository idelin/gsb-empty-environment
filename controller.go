// Package thermosim simulates thermostatic switch controllers driven by
// explicit temperature samples, plus an arbiter that shares a global
// maximum number of simultaneously-On controllers.
//
// Semantics:
//   - Decisions are made only at explicit sample times; the simulator
//     never interpolates or invents samples.
//   - temp > Upper demands Off, temp < Lower demands On, anything in
//     between (inclusive) holds the current state. With Upper == Lower,
//     an exact reading therefore holds.
//   - After each switch a minimum dwell time must elapse before the next
//     switch; a demanded switch that arrives too early is deferred and
//     re-judged from scratch at the next sample.
//   - Samples must carry strictly increasing times; anything else is
//     rejected as late and leaves the controller untouched.
package thermosim

import "errors"

// ErrLateSample is returned when a sample does not advance the sample
// clock of the controller (time must be strictly increasing).
var ErrLateSample = errors.New("thermosim: late sample rejected")

// State is the switch state of a controller.
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

// Sample is an explicit temperature reading at a logical time.
type Sample struct {
	Time int64
	Temp float64
}

// Decision describes the outcome of processing one sample.
type Decision int

const (
	// Hold: no switch required (or demanded while already in that state).
	Hold Decision = iota
	// SwitchedOn: the controller switched Off -> On at this sample.
	SwitchedOn
	// SwitchedOff: the controller switched On -> Off at this sample.
	SwitchedOff
	// Deferred: a switch was demanded but the minimum dwell time has not
	// elapsed; the decision is postponed to the next sample.
	Deferred
	// WaitCapacity: a switch to On was demanded and locally safe, but the
	// shared capacity is exhausted; the request stays pending.
	WaitCapacity
)

func (d Decision) String() string {
	switch d {
	case Hold:
		return "Hold"
	case SwitchedOn:
		return "SwitchedOn"
	case SwitchedOff:
		return "SwitchedOff"
	case Deferred:
		return "Deferred"
	case WaitCapacity:
		return "WaitCapacity"
	}
	return "Unknown"
}

// Controller is a thermostatic switch with hysteresis and minimum dwell.
type Controller struct {
	Name     string
	Lower    float64 // temp < Lower demands On
	Upper    float64 // temp > Upper demands Off; Lower <= temp <= Upper holds
	MinDwell int64   // minimum logical time between two switches

	// Priority resolves shared-capacity contention; smaller wins.
	// Ties break by request sequence number. Set by Arbiter.Attach.
	Priority int

	state       State
	lastSwitch  int64
	hasSwitched bool
	lastSample  int64
	hasSample   bool

	arbiter  *Arbiter
	hasGrant bool
	pending  bool
	reqSeq   int64
}

// NewController creates a controller in the given initial state. The
// initial state is not a switch, so the first real switch is not
// constrained by MinDwell.
func NewController(name string, lower, upper float64, minDwell int64, initial State) *Controller {
	return &Controller{
		Name:     name,
		Lower:    lower,
		Upper:    upper,
		MinDwell: minDwell,
		state:    initial,
	}
}

// State returns the current switch state.
func (c *Controller) State() State { return c.state }

// Pending reports whether the controller is waiting for shared capacity
// to switch On.
func (c *Controller) Pending() bool { return c.pending }

// LastSwitch returns the time of the last actual switch, if any.
func (c *Controller) LastSwitch() (int64, bool) { return c.lastSwitch, c.hasSwitched }

// target maps a temperature to the demanded state; the hysteresis band
// (inclusive) demands the current state, i.e. hold.
func (c *Controller) target(temp float64) State {
	switch {
	case temp > c.Upper:
		return Off
	case temp < c.Lower:
		return On
	default:
		return c.state
	}
}

func (c *Controller) dwellSatisfied(t int64) bool {
	return !c.hasSwitched || t-c.lastSwitch >= c.MinDwell
}

func (c *Controller) switchTo(st State, t int64) {
	c.state = st
	c.lastSwitch = t
	c.hasSwitched = true
}

// Process evaluates one explicit sample and returns the decision taken.
// A late sample (Time not greater than the previous one) is rejected
// with ErrLateSample and changes nothing.
func (c *Controller) Process(s Sample) (Decision, error) {
	if c.hasSample && s.Time <= c.lastSample {
		return Hold, ErrLateSample
	}
	c.hasSample = true
	c.lastSample = s.Time

	tgt := c.target(s.Temp)

	// Local safety: a pending capacity request is only kept while the
	// latest sample still demands On.
	if tgt == Off && c.pending {
		c.arbiter.withdraw(c)
	}

	switch {
	case tgt == c.state:
		return Hold, nil
	case !c.dwellSatisfied(s.Time):
		return Deferred, nil
	case tgt == On:
		if c.arbiter != nil {
			if !c.arbiter.request(c, s.Time) {
				return WaitCapacity, nil
			}
		} else {
			c.switchTo(On, s.Time)
		}
		return SwitchedOn, nil
	default: // tgt == Off
		if c.arbiter != nil {
			c.arbiter.release(c, s.Time)
		}
		c.switchTo(Off, s.Time)
		return SwitchedOff, nil
	}
}
