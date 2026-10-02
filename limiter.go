// Package eventlimiter implements an event-driven precise rate-limiting
// bucket (sliding-window token/leaky bucket).
//
// Requests are judged by their event occurrence time, not their physical
// arrival time. Historical event timestamps are kept in sorted order. When
// a late-arriving event is submitted, the legality of all subsequent events
// is re-evaluated and revocation decisions are emitted for events that no
// longer fit within the limit.
package eventlimiter

import (
	"fmt"
	"sort"
)

// Event is a request identified by ID, occurring at Time (event time, in
// the same unit as the limiter's window, e.g. seconds).
type Event struct {
	ID   string
	Time int64
}

// DecisionType describes the outcome of (re-)evaluating an event.
type DecisionType int

const (
	// Accepted: the event fits within the limit.
	Accepted DecisionType = iota
	// Rejected: the event exceeds the limit.
	Rejected
	// Revoked: the event was previously accepted but a late-arriving event
	// re-evaluated the window and it now exceeds the limit.
	Revoked
	// Reinstated: the event was previously rejected but now fits again.
	Reinstated
)

func (d DecisionType) String() string {
	switch d {
	case Accepted:
		return "ACCEPTED"
	case Rejected:
		return "REJECTED"
	case Revoked:
		return "REVOKED"
	case Reinstated:
		return "REINSTATED"
	}
	return "UNKNOWN"
}

// Decision is the notification produced for an event after evaluation.
type Decision struct {
	Event  Event
	Type   DecisionType
	Reason string
}

func (d Decision) String() string {
	return fmt.Sprintf("[%s] event=%s t=%d (%s)", d.Type, d.Event.ID, d.Event.Time, d.Reason)
}

// Limiter is a precise sliding-window rate limiter: at most Limit events
// are allowed within any Window-length interval, judged by event time.
// Rejected events do not consume quota (leaky-bucket semantics).
type Limiter struct {
	Limit  int
	Window int64

	events  []Event         // kept sorted by (Time, insertion order)
	allowed map[string]bool // current legality per event ID
	seq     int64
	stamp   map[string]int64 // insertion sequence for stable ordering
}

// New creates a Limiter allowing at most limit events per window.
func New(limit int, window int64) *Limiter {
	return &Limiter{
		Limit:   limit,
		Window:  window,
		allowed: make(map[string]bool),
		stamp:   make(map[string]int64),
	}
}

// Submit records an event (which may arrive late, i.e. out of order),
// re-evaluates the legality of all retained events in event-time order,
// and returns the decisions produced: the decision for the submitted event
// plus a correction (Revoked/Reinstated) for every previously known event
// whose legality changed as a consequence.
func (l *Limiter) Submit(e Event) []Decision {
	l.seq++
	l.stamp[e.ID] = l.seq
	idx := sort.Search(len(l.events), func(i int) bool {
		if l.events[i].Time != e.Time {
			return l.events[i].Time > e.Time
		}
		return l.stamp[l.events[i].ID] > l.stamp[e.ID]
	})
	l.events = append(l.events, Event{})
	copy(l.events[idx+1:], l.events[idx:])
	l.events[idx] = e

	nowAllowed := l.evaluate()

	var decisions []Decision
	for _, ev := range l.events {
		was, known := l.allowed[ev.ID]
		is := nowAllowed[ev.ID]
		switch {
		case ev.ID == e.ID && !known:
			d := Decision{Event: ev, Type: Accepted, Reason: "within limit"}
			if !is {
				d = Decision{Event: ev, Type: Rejected, Reason: "exceeds limit"}
			}
			decisions = append(decisions, d)
		case known && was && !is:
			decisions = append(decisions, Decision{
				Event:  ev,
				Type:   Revoked,
				Reason: "late event re-evaluation pushed it over the limit",
			})
		case known && !was && is:
			decisions = append(decisions, Decision{
				Event:  ev,
				Type:   Reinstated,
				Reason: "re-evaluation freed quota",
			})
		}
	}
	l.allowed = nowAllowed
	return decisions
}

// evaluate replays events in event-time order and determines which are
// allowed: an event at time t is allowed if fewer than Limit already
// allowed events fall in the sliding window (t-Window, t].
func (l *Limiter) evaluate() map[string]bool {
	allowed := make(map[string]bool, len(l.events))
	var granted []Event
	for _, ev := range l.events {
		cutoff := ev.Time - l.Window
		n := 0
		for _, g := range granted {
			if g.Time > cutoff {
				n++
			}
		}
		if n < l.Limit {
			allowed[ev.ID] = true
			granted = append(granted, ev)
		}
	}
	return allowed
}

// Allowed reports whether the event with the given ID is currently allowed.
func (l *Limiter) Allowed(id string) bool { return l.allowed[id] }

// Events returns the retained event history in event-time order.
func (l *Limiter) Events() []Event {
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}
