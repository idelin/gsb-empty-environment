// Package alertjoin implements a keyed interval join over two alert
// streams. Two events match when they share a key and their event-time
// difference is at most d. State is reclaimed only when the *opposite*
// side's watermark passes an event's time plus d.
package alertjoin

import (
	"fmt"
	"math"
	"sort"
)

// Side identifies one of the two input streams.
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Left {
		return "left"
	}
	return "right"
}

func (s Side) other() Side { return 1 - s }

// Event is a single alert on one of the two streams.
type Event struct {
	ID      string
	Key     string
	Time    int64 // event time
	Payload string
}

func (e Event) sameContent(o Event) bool {
	return e.Key == o.Key && e.Time == o.Time && e.Payload == o.Payload
}

// Pair is one matched (left, right) event combination. Each pair is
// emitted exactly once, when the later of its two events arrives.
type Pair struct {
	Left  Event
	Right Event
}

// ConflictError is returned when an event ID reappears on the same side
// with different content.
type ConflictError struct {
	Side     Side
	ID       string
	Existing Event
	Incoming Event
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("alertjoin: conflicting %s event id %q: existing %+v, incoming %+v",
		e.Side, e.ID, e.Existing, e.Incoming)
}

// Joiner holds the join state for both streams.
//
// Watermark contract: after UpdateWatermark(s, w), no more events with
// Time <= w will arrive on side s. Events violating this are late and are
// dropped silently. The two sides' watermarks may advance independently
// (they are not required to be synchronized).
//
// Two kinds of state are kept per side, reclaimed by different watermarks:
//   - match state (buf) is reclaimed once the *opposite* watermark passes
//     event time + d, after which no match is possible;
//   - dedup state (seen) is reclaimed once the side's *own* watermark
//     passes event time, after which any re-delivery is late and dropped
//     before it is consulted.
type Joiner struct {
	d     int64
	buf   [2]map[string]Event // live, deduplicated events by ID, per side
	seen  [2]map[string]Event // first-seen content by ID, per side
	wm    [2]int64            // current watermark per side
	pairs []Pair              // all pairs emitted so far
}

// New creates a Joiner matching events whose time difference is at most d.
func New(d int64) *Joiner {
	if d < 0 {
		panic("alertjoin: negative interval")
	}
	j := &Joiner{d: d}
	j.buf[Left] = make(map[string]Event)
	j.buf[Right] = make(map[string]Event)
	j.seen[Left] = make(map[string]Event)
	j.seen[Right] = make(map[string]Event)
	j.wm[Left] = math.MinInt64
	j.wm[Right] = math.MinInt64
	return j
}

// AddEvent ingests one event on side s and returns any newly emitted
// pairs. A re-delivered ID with identical content is ignored; one with
// different content yields a *ConflictError.
func (j *Joiner) AddEvent(s Side, e Event) ([]Pair, error) {
	if e.Time <= j.wm[s] {
		return nil, nil // late event, past the side's watermark
	}
	if prev, ok := j.seen[s][e.ID]; ok {
		if prev.sameContent(e) {
			return nil, nil // exact duplicate, ignore
		}
		return nil, &ConflictError{Side: s, ID: e.ID, Existing: prev, Incoming: e}
	}
	j.seen[s][e.ID] = e
	j.buf[s][e.ID] = e

	var out []Pair
	for _, o := range j.buf[s.other()] {
		if o.Key != e.Key || absDiff(o.Time, e.Time) > j.d {
			continue
		}
		if s == Left {
			out = append(out, Pair{Left: e, Right: o})
		} else {
			out = append(out, Pair{Left: o, Right: e})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Left.ID != out[b].Left.ID {
			return out[a].Left.ID < out[b].Left.ID
		}
		return out[a].Right.ID < out[b].Right.ID
	})
	j.pairs = append(j.pairs, out...)
	return out, nil
}

// UpdateWatermark advances side s's watermark to wm (non-monotonic updates
// are ignored) and reclaims buffered events on the *opposite* side whose
// time plus d has been passed by the new watermark: such an event can
// never match again, because every future event on side s has
// Time > wm > event.Time + d.
func (j *Joiner) UpdateWatermark(s Side, wm int64) {
	if wm <= j.wm[s] {
		return
	}
	j.wm[s] = wm
	limit := int64(math.MinInt64)
	if wm >= math.MinInt64+j.d { // guard against underflow
		limit = wm - j.d
	}
	for id, e := range j.buf[s.other()] {
		if e.Time < limit {
			delete(j.buf[s.other()], id)
		}
	}
	// Dedup state on side s itself is reclaimed once the side's own
	// watermark passes the event time: any re-delivery is then late and
	// dropped before the dedup check.
	for id, e := range j.seen[s] {
		if e.Time <= wm {
			delete(j.seen[s], id)
		}
	}
}

// Pairs returns all pairs emitted so far, in emission order.
func (j *Joiner) Pairs() []Pair {
	out := make([]Pair, len(j.pairs))
	copy(out, j.pairs)
	return out
}

// Buffered reports how many events are currently held for side s.
func (j *Joiner) Buffered(s Side) int { return len(j.buf[s]) }

// Watermark reports the current watermark of side s.
func (j *Joiner) Watermark(s Side) int64 { return j.wm[s] }

func absDiff(a, b int64) int64 {
	if a < b {
		return b - a
	}
	return a - b
}
