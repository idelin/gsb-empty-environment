// Package cep implements a multi-mode composite event sequence matcher
// built on a non-deterministic acyclic graph (NFA) of pattern states.
//
// Supported pattern: A -> B with negation of C in between, inside a
// sliding event-time window. Out-of-order events trigger compensating
// retractions; expired partial runs are discarded on timeout.
package cep

import "fmt"

// Event is a single occurrence in the input stream. Time is the
// event-time (seconds); events may arrive out of order.
type Event struct {
	Type string
	Time int64
}

// Match is a completed A -> B pair.
type Match struct {
	A Event
	B Event
}

// NotificationKind distinguishes hits from compensating retractions.
type NotificationKind string

const (
	NotifyMatch   NotificationKind = "MATCH"
	NotifyRetract NotificationKind = "RETRACT"
)

// Notification is emitted when a match is produced or revoked.
type Notification struct {
	Kind      NotificationKind
	Match     Match
	EmittedAt int64 // engine clock (max event-time seen) when emitted
	CausedBy  Event // the event that triggered this notification
}

// runState is a node in the NFA: each live A spawns a run that may
// transition to "completed" on B, or be killed by C / timeout.
type run struct {
	a         Event
	b         *Event
	retracted bool
	dead      bool // invalidated by C or expired by timeout
}

// Engine is the CEP matcher for pattern: A -> B, no C between, within Window seconds.
type Engine struct {
	Window        int64
	now           int64
	started       bool
	runs          []*run
	notifications []Notification
	log           []string
}

// NewEngine creates an engine with the given sliding window in seconds.
func NewEngine(windowSeconds int64) *Engine {
	return &Engine{Window: windowSeconds}
}

// Process feeds one event into the NFA. Events may arrive out of order;
// the engine clock advances to the max event-time seen so far.
func (e *Engine) Process(ev Event) {
	if !e.started || ev.Time > e.now {
		e.now = ev.Time
		e.started = true
	}
	e.expireRuns()

	switch ev.Type {
	case "A":
		e.runs = append(e.runs, &run{a: ev})
		e.logf("t=%d run started by A@%d", e.now, ev.Time)
	case "B":
		e.onB(ev)
	case "C":
		e.onC(ev)
	}
}

// onB completes every live pending run whose A is within the window.
func (e *Engine) onB(b Event) {
	for _, r := range e.runs {
		if r.dead || r.b != nil {
			continue
		}
		if b.Time < r.a.Time || b.Time-r.a.Time > e.Window {
			continue
		}
		bCopy := b
		r.b = &bCopy
		m := Match{A: r.a, B: b}
		e.notifications = append(e.notifications, Notification{
			Kind: NotifyMatch, Match: m, EmittedAt: e.now, CausedBy: b,
		})
		e.logf("t=%d MATCH A@%d -> B@%d (window %ds)", e.now, r.a.Time, b.Time, e.Window)
	}
}

// onC invalidates runs whose interval contains the C event. A late C
// falling inside an already-matched (A, B) pair triggers a retraction.
func (e *Engine) onC(c Event) {
	for _, r := range e.runs {
		if r.dead || c.Time <= r.a.Time {
			continue
		}
		if r.b == nil {
			r.dead = true
			e.logf("t=%d run A@%d killed by C@%d (negation)", e.now, r.a.Time, c.Time)
			continue
		}
		if c.Time < r.b.Time && !r.retracted {
			r.retracted = true
			r.dead = true
			m := Match{A: r.a, B: *r.b}
			e.notifications = append(e.notifications, Notification{
				Kind: NotifyRetract, Match: m, EmittedAt: e.now, CausedBy: c,
			})
			e.logf("t=%d RETRACT A@%d -> B@%d: late C@%d arrived inside interval (compensation)",
				e.now, r.a.Time, r.b.Time, c.Time)
		}
	}
}

// expireRuns discards pending runs whose window has elapsed (timeout).
func (e *Engine) expireRuns() {
	for _, r := range e.runs {
		if r.dead || r.b != nil {
			continue
		}
		if r.a.Time+e.Window < e.now {
			r.dead = true
			e.logf("t=%d run A@%d discarded: window %ds expired (timeout)", e.now, r.a.Time, e.Window)
		}
	}
}

// Notifications returns all emitted notifications in order.
func (e *Engine) Notifications() []Notification {
	out := make([]Notification, len(e.notifications))
	copy(out, e.notifications)
	return out
}

// Log returns the engine's internal audit log.
func (e *Engine) Log() []string {
	out := make([]string, len(e.log))
	copy(out, e.log)
	return out
}

func (e *Engine) logf(format string, args ...any) {
	e.log = append(e.log, fmt.Sprintf(format, args...))
}
