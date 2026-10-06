// Package alertjoin implements a two-way alert join connector.
//
// Two alert streams (left and right) are joined by key (alert id): a pair
// is emitted when both sides carry the same id and their event times differ
// by at most d. Each side tracks its own watermark; a buffered event may be
// reclaimed only once the OPPOSITE side's watermark has passed
// event.Time + d, because no later event from the opposite side could then
// still match it.
//
// Semantics:
//   - Same id re-delivered with identical content is a duplicate: ignored.
//   - Same id re-delivered with different content is a conflict: error.
//   - Every matched pair is emitted exactly once, on arrival of whichever
//     side completes the pair.
//   - Dedup/conflict state (tombstones) is kept until the side's OWN
//     watermark passes the event time, because a duplicate may legally
//     arrive until then; the matching buffer is reclaimed when the
//     OPPOSITE watermark passes event time + d.
//   - Matching guarantees hold as long as each side's events respect its own
//     watermark (event time >= current watermark). Watermarks of the two
//     sides may advance independently (non-synchronized).
package alertjoin

import "fmt"

// Event is a single alert from either side of the join.
type Event struct {
	ID      string
	Time    int64
	Content string
}

// Pair is one matched alert pair. Left and Right preserve the side the
// events arrived on.
type Pair struct {
	Left  Event
	Right Event
}

// ConflictError reports a re-delivered id whose content differs from the
// event already buffered for that id.
type ConflictError struct {
	ID       string
	Existing Event
	Incoming Event
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("alertjoin: conflicting content for id %q: have %+v, got %+v",
		e.ID, e.Existing, e.Incoming)
}

const (
	left  = 0
	right = 1
)

type sideState struct {
	events    map[string]Event // match buffer, keyed by id
	seen      map[string]Event // dedup/conflict tombstones, keyed by id
	watermark int64
}

// Joiner is the two-way alert join connector.
type Joiner struct {
	d     int64
	sides [2]*sideState
	pairs []Pair
}

// NewJoiner returns a Joiner that matches events whose times differ by at
// most d (d >= 0).
func NewJoiner(d int64) *Joiner {
	if d < 0 {
		panic("alertjoin: negative join window")
	}
	return &Joiner{
		d: d,
		sides: [2]*sideState{
			{events: make(map[string]Event), seen: make(map[string]Event)},
			{events: make(map[string]Event), seen: make(map[string]Event)},
		},
	}
}

// AddLeft ingests one event from the left stream.
func (j *Joiner) AddLeft(ev Event) error { return j.add(left, ev) }

// AddRight ingests one event from the right stream.
func (j *Joiner) AddRight(ev Event) error { return j.add(right, ev) }

// AdvanceLeftWatermark moves the left watermark forward and reclaims
// right-side events that can no longer be matched.
func (j *Joiner) AdvanceLeftWatermark(ts int64) { j.advance(left, ts) }

// AdvanceRightWatermark moves the right watermark forward and reclaims
// left-side events that can no longer be matched.
func (j *Joiner) AdvanceRightWatermark(ts int64) { j.advance(right, ts) }

// Pairs returns the matched pairs in emission order.
func (j *Joiner) Pairs() []Pair {
	out := make([]Pair, len(j.pairs))
	copy(out, j.pairs)
	return out
}

// Buffered reports how many events are still held on each side.
func (j *Joiner) Buffered() (leftCount, rightCount int) {
	return len(j.sides[left].events), len(j.sides[right].events)
}

// Watermarks reports the current watermark of each side.
func (j *Joiner) Watermarks() (leftWM, rightWM int64) {
	return j.sides[left].watermark, j.sides[right].watermark
}

func (j *Joiner) add(side int, ev Event) error {
	self := j.sides[side]
	if existing, ok := self.seen[ev.ID]; ok {
		if existing != ev {
			return &ConflictError{ID: ev.ID, Existing: existing, Incoming: ev}
		}
		return nil // exact duplicate: ignore
	}
	self.seen[ev.ID] = ev
	self.events[ev.ID] = ev

	other := j.sides[1-side]
	if match, ok := other.events[ev.ID]; ok && abs(match.Time-ev.Time) <= j.d {
		if side == left {
			j.pairs = append(j.pairs, Pair{Left: ev, Right: match})
		} else {
			j.pairs = append(j.pairs, Pair{Left: match, Right: ev})
		}
	}
	return nil
}

func (j *Joiner) advance(side int, ts int64) {
	self := j.sides[side]
	if ts <= self.watermark {
		return // watermarks are monotone
	}
	self.watermark = ts

	// Reclaim opposite-side events that no future event from this side can
	// still match: this side's watermark passed their time + d.
	other := j.sides[1-side]
	for id, ev := range other.events {
		if ev.Time+j.d < ts {
			delete(other.events, id)
		}
	}
	// Reclaim own tombstones whose duplicates can no longer arrive: this
	// side's watermark passed their event time.
	for id, ev := range self.seen {
		if ev.Time < ts {
			delete(self.seen, id)
		}
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
