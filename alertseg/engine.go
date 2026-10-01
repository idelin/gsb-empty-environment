// Package alertseg collapses a stream of raw alert state changes into
// event segments and decides when notifications should be emitted.
//
// The engine is driven entirely by virtual time: callers feed telemetry
// events via Apply and advance the clock via AdvanceTo. No wall clock is
// ever read, which makes behavior fully deterministic and testable.
//
// Notification policy:
//   - A transition into Firing emits a Firing notification immediately,
//     unless a suppression window is active.
//   - While a notified fault stays active, a Repeat notification is
//     emitted every RepeatInterval.
//   - A transition back to OK emits a Resolved notification, but only if
//     the segment was ever notified.
//   - During suppression the real state is still tracked, but nothing is
//     emitted. When suppression is lifted, a fault that is still active
//     is notified (Firing if never notified, Repeat otherwise). A fault
//     that recovered while suppressed is closed silently: the stale
//     fault is never backfilled.
package alertseg

import (
	"fmt"
	"time"
)

// State is the health state reported by telemetry.
type State int

const (
	OK State = iota
	Firing
)

func (s State) String() string {
	if s == Firing {
		return "firing"
	}
	return "ok"
}

// ParseState parses "ok" / "firing".
func ParseState(s string) (State, error) {
	switch s {
	case "ok":
		return OK, nil
	case "firing":
		return Firing, nil
	}
	return OK, fmt.Errorf("unknown state %q", s)
}

// Event is a single raw telemetry state observation.
type Event struct {
	At    time.Time
	State State
}

// Kind classifies a notification.
type Kind int

const (
	KindFiring Kind = iota
	KindRepeat
	KindResolved
)

func (k Kind) String() string {
	switch k {
	case KindFiring:
		return "FIRING"
	case KindRepeat:
		return "REPEAT"
	case KindResolved:
		return "RESOLVED"
	}
	return "UNKNOWN"
}

// Notification is an emitted alert message.
type Notification struct {
	At    time.Time
	Kind  Kind
	Since time.Time // start of the fault segment this notification belongs to
}

func (n Notification) String() string {
	return fmt.Sprintf("%s %-8s fault_since=%s",
		n.At.Format(time.RFC3339), n.Kind, n.Since.Format(time.RFC3339))
}

// Segment is a maximal run of a constant state. End is zero while the
// segment is still open.
type Segment struct {
	State State
	Start time.Time
	End   time.Time
}

func (s Segment) String() string {
	end := "ongoing"
	if !s.End.IsZero() {
		end = s.End.Format(time.RFC3339)
	}
	return fmt.Sprintf("%-6s %s -> %s", s.State, s.Start.Format(time.RFC3339), end)
}

// Window is a half-open suppression interval [Start, End).
type Window struct {
	Start time.Time
	End   time.Time
}

// Engine folds events into segments and emits notifications.
type Engine struct {
	repeat  time.Duration
	windows []Window
	wi      int // index of the next suppression window to enter

	now time.Time

	state    State
	segStart time.Time
	hasSeg   bool
	notified bool // current fault segment has been notified at least once

	suppressed    bool
	suppressUntil time.Time

	repeatSet  bool
	nextRepeat time.Time

	segments []Segment
	out      []Notification
}

// NewEngine builds an engine. repeat is the re-notification interval for
// ongoing faults (<=0 disables repeats). windows must be sorted by Start
// and non-overlapping.
func NewEngine(repeat time.Duration, windows []Window) *Engine {
	return &Engine{repeat: repeat, windows: windows}
}

// Notifications returns the notifications emitted so far.
func (e *Engine) Notifications() []Notification {
	out := make([]Notification, len(e.out))
	copy(out, e.out)
	return out
}

// Segments returns the completed segments plus the still-open one.
func (e *Engine) Segments() []Segment {
	out := make([]Segment, len(e.segments))
	copy(out, e.segments)
	if e.hasSeg {
		out = append(out, Segment{State: e.state, Start: e.segStart})
	}
	return out
}

// Apply folds one telemetry event into the engine. Events must arrive in
// non-decreasing time order.
func (e *Engine) Apply(ev Event) {
	e.advance(ev.At, false) // timers due exactly at ev.At fire after the state change
	if e.hasSeg && ev.State == e.state {
		return // duplicate observation: merged into the open segment
	}
	// Close the outgoing segment.
	if e.hasSeg {
		e.segments = append(e.segments, Segment{State: e.state, Start: e.segStart, End: ev.At})
	}
	// A closing fault segment gets a Resolved notification only if it was
	// ever notified, and never while suppressed. A fault that started and
	// recovered inside suppression is closed silently: the stale fault is
	// not backfilled.
	if e.hasSeg && e.state == Firing && e.notified && !e.suppressed {
		e.emit(KindResolved)
	}
	e.repeatSet = false
	e.hasSeg = true
	e.segStart = ev.At
	e.state = ev.State
	e.notified = false
	if ev.State == Firing && !e.suppressed {
		e.emit(KindFiring)
		e.scheduleRepeat()
	}
}

// AdvanceTo moves virtual time forward, firing any scheduled timers
// (suppression window entry/exit, repeat notifications) up to and
// including t.
func (e *Engine) AdvanceTo(t time.Time) {
	e.advance(t, true)
}

// advance moves virtual time to t. When inclusive is false, timers
// scheduled exactly at t are left pending. Suppression boundaries are
// always inclusive: a window [Start, End) governs events stamped with
// Start, and no longer governs events stamped with End.
func (e *Engine) advance(t time.Time, inclusive bool) {
	dueBoundary := func(x time.Time) bool { return !x.After(t) }
	dueRepeat := func(x time.Time) bool {
		if inclusive {
			return !x.After(t)
		}
		return x.Before(t)
	}
	for {
		var next time.Time
		have := false
		consider := func(x time.Time) {
			if !have || x.Before(next) {
				next, have = x, true
			}
		}
		if e.wi < len(e.windows) && dueBoundary(e.windows[e.wi].Start) {
			consider(e.windows[e.wi].Start)
		}
		if e.suppressed && dueBoundary(e.suppressUntil) {
			consider(e.suppressUntil)
		}
		if e.repeatSet && dueRepeat(e.nextRepeat) {
			consider(e.nextRepeat)
		}
		if !have {
			break
		}
		e.now = next
		if e.wi < len(e.windows) && !e.now.Before(e.windows[e.wi].Start) {
			e.suppressed = true
			e.suppressUntil = e.windows[e.wi].End
			e.wi++
			continue
		}
		if e.suppressed && !e.now.Before(e.suppressUntil) {
			e.suppressed = false
			e.liftSuppression()
		}
		if e.repeatSet && !e.now.Before(e.nextRepeat) {
			e.fireRepeat()
		}
	}
	e.now = t
}

// liftSuppression runs when a suppression window ends. The real state was
// tracked throughout, so a fault that is still active is notified now;
// a fault that recovered during suppression left state == OK and gets
// nothing.
func (e *Engine) liftSuppression() {
	if e.state != Firing || !e.hasSeg {
		return
	}
	kind := KindFiring
	if e.notified {
		kind = KindRepeat
	}
	e.emit(kind)
	e.scheduleRepeat()
}

func (e *Engine) fireRepeat() {
	e.repeatSet = false
	if e.state == Firing && e.notified && !e.suppressed {
		e.emit(KindRepeat)
		e.scheduleRepeat()
	}
}

func (e *Engine) emit(k Kind) {
	e.out = append(e.out, Notification{At: e.now, Kind: k, Since: e.segStart})
	e.notified = true
}

func (e *Engine) scheduleRepeat() {
	if e.repeat > 0 {
		e.nextRepeat = e.now.Add(e.repeat)
		e.repeatSet = true
	}
}
