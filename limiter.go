// Package eventlimiter implements an event-driven exact rate-limiting bucket.
//
// The limiter keeps the full, time-ordered history of request timestamps and
// judges every request by its event time (when it happened) rather than its
// physical arrival time. When a late-arriving request is submitted, the
// limiter re-evaluates the legality of all subsequent requests and emits
// revocation (correction) decisions for any request whose verdict flips.
package eventlimiter

import (
	"fmt"
	"sort"
	"time"
)

// Verdict is the limiting decision for a single request.
type Verdict int

const (
	// Rejected means the request exceeds the rate limit.
	Rejected Verdict = iota
	// Accepted means the request is within the rate limit.
	Accepted
)

func (v Verdict) String() string {
	if v == Accepted {
		return "ACCEPTED"
	}
	return "REJECTED"
}

// Correction describes a verdict change for a previously judged request,
// emitted when a late-arriving event reshapes the window.
type Correction struct {
	ID     string
	Time   time.Time
	From   Verdict
	To     Verdict
	Reason string
}

func (c Correction) String() string {
	return fmt.Sprintf("correction: request %q (t=%s) %s -> %s (%s)",
		c.ID, c.Time.Format("15:04:05"), c.From, c.To, c.Reason)
}

// record is one remembered request.
type record struct {
	id      string
	t       time.Time // event time, not arrival time
	verdict Verdict
}

// Limiter is an exact sliding-window rate-limiting bucket: at most max
// requests are accepted within any window-length span of event time.
type Limiter struct {
	max    int
	window time.Duration

	// history holds every submitted request ordered by event time.
	history []record
}

// New creates a Limiter allowing at most max requests per window of event time.
func New(max int, window time.Duration) *Limiter {
	if max <= 0 {
		panic("eventlimiter: max must be positive")
	}
	if window <= 0 {
		panic("eventlimiter: window must be positive")
	}
	return &Limiter{max: max, window: window}
}

// Submit registers a request identified by id that occurred at event time t.
//
// It returns the verdict for the submitted request plus one Correction for
// every previously judged request whose verdict changed as a consequence of
// this submission (e.g. a late arrival evicting a later request from the
// window quota).
func (l *Limiter) Submit(id string, t time.Time) (Verdict, []Correction) {
	// Insert keeping history ordered by event time; ties keep arrival order.
	pos := sort.Search(len(l.history), func(i int) bool {
		return l.history[i].t.After(t)
	})
	l.history = append(l.history, record{})
	copy(l.history[pos+1:], l.history[pos:])
	l.history[pos] = record{id: id, t: t, verdict: Rejected}

	corrections := l.reevaluate(pos)
	return l.history[pos].verdict, corrections
}

// reevaluate replays the ordered history and recomputes the exact
// sliding-window verdict of every request, emitting corrections for flips.
// The record at index self is the just-submitted request: its flip from the
// Rejected placeholder is its own verdict, not a correction of a prior
// decision, so it is excluded from the returned corrections.
func (l *Limiter) reevaluate(self int) []Correction {
	var corrections []Correction
	for i := range l.history {
		rec := &l.history[i]
		// Count already-accepted requests inside (t-window, t).
		accepted := 0
		for j := i - 1; j >= 0; j-- {
			prev := l.history[j]
			if !prev.t.After(rec.t.Add(-l.window)) {
				break // outside the window; history is ordered
			}
			if prev.verdict == Accepted {
				accepted++
			}
		}
		next := Rejected
		if accepted < l.max {
			next = Accepted
		}
		if next != rec.verdict {
			if i != self {
				corrections = append(corrections, Correction{
					ID:   rec.id,
					Time: rec.t,
					From: rec.verdict,
					To:   next,
					Reason: fmt.Sprintf("late arrival reshaped window: %d/%d slots taken in preceding %s",
						accepted, l.max, l.window),
				})
			}
			rec.verdict = next
		}
	}
	return corrections
}

// VerdictOf reports the current verdict of a previously submitted request.
func (l *Limiter) VerdictOf(id string) (Verdict, bool) {
	for _, rec := range l.history {
		if rec.id == id {
			return rec.verdict, true
		}
	}
	return Rejected, false
}

// Entry is one item of the ordered event-time history.
type Entry struct {
	ID      string
	Time    time.Time
	Verdict Verdict
}

// History returns the ordered event-time history with current verdicts.
func (l *Limiter) History() []Entry {
	out := make([]Entry, len(l.history))
	for i, rec := range l.history {
		out[i] = Entry{ID: rec.id, Time: rec.t, Verdict: rec.verdict}
	}
	return out
}
