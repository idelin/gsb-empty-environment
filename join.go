// Package streamjoin implements a keyed, event-time interval join over two
// streams with independently advancing watermarks.
//
// Semantics:
//   - A left event l and a right event r with the same key are connected when
//     r.Timestamp lies in [l.Timestamp+LowerBound, l.Timestamp+UpperBound].
//   - Each side keeps its own watermark; state of one side is cleaned only
//     based on the OTHER side's watermark, so a stalled side never causes
//     premature deletion of matchable data on the opposite side.
//   - Events arriving behind their own side's watermark are late. If they are
//     still within AllowedLateness (the correction period) they are joined
//     normally and the resulting pairs are flagged as deltas. Events beyond
//     the correction period are not joined; they are reported separately.
package streamjoin

import "math"

// Side identifies one of the two joined streams.
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

// Event is a single record of either input stream.
type Event struct {
	Key       string
	Timestamp int64 // event time, milliseconds
	Value     interface{}
}

// Output is one joined pair. Delta is true when the pair was produced by a
// late-but-accepted event, i.e. it is a correction appended after results for
// the same window may already have been emitted downstream.
type Output struct {
	Left  *Event
	Right *Event
	Delta bool
}

// LateRecord describes an event that fell outside the correction period and
// was therefore excluded from the join.
type LateRecord struct {
	Side  Side
	Event *Event
}

// Config configures a Joiner.
type Config struct {
	// LowerBound and UpperBound define the join window relative to the left
	// event time: a right event r matches a left event l when
	// l.Timestamp+LowerBound <= r.Timestamp <= l.Timestamp+UpperBound.
	LowerBound int64
	UpperBound int64
	// AllowedLateness is the length of the correction period. Events arriving
	// at most this far behind their side's watermark are still joined (as
	// deltas); later events are reported as late.
	AllowedLateness int64
}

// Joiner holds the join state for both streams.
type Joiner struct {
	cfg Config

	watermark [2]int64
	state     [2]map[string][]*Event

	outputs []Output
	late    []LateRecord
}

// New creates a Joiner. Both watermarks start at the minimum possible value.
func New(cfg Config) *Joiner {
	return &Joiner{
		cfg:       cfg,
		watermark: [2]int64{math.MinInt64, math.MinInt64},
		state:     [2]map[string][]*Event{make(map[string][]*Event), make(map[string][]*Event)},
	}
}

// Watermark returns the current watermark of the given side.
func (j *Joiner) Watermark(side Side) int64 {
	return j.watermark[side]
}

// Outputs returns all joined pairs emitted so far, in emission order.
func (j *Joiner) Outputs() []Output {
	return j.outputs
}

// LateRecords returns all events rejected for exceeding the correction period.
func (j *Joiner) LateRecords() []LateRecord {
	return j.late
}

// StateSize returns the number of buffered events on the given side.
func (j *Joiner) StateSize(side Side) int {
	n := 0
	for _, evs := range j.state[side] {
		n += len(evs)
	}
	return n
}

// Add ingests one event on the given side, joins it against the buffered
// events of the opposite side and buffers it for future matches.
func (j *Joiner) Add(side Side, e *Event) {
	other := 1 - side

	delta := false
	if e.Timestamp <= j.watermark[side] {
		// Late relative to this side's own watermark.
		if e.Timestamp+j.cfg.AllowedLateness < j.watermark[side] {
			j.late = append(j.late, LateRecord{Side: side, Event: e})
			return
		}
		delta = true
	}

	for _, o := range j.state[other][e.Key] {
		var l, r *Event
		if side == Left {
			l, r = e, o
		} else {
			l, r = o, e
		}
		if j.matches(l, r) {
			j.outputs = append(j.outputs, Output{Left: l, Right: r, Delta: delta})
		}
	}

	j.state[side][e.Key] = append(j.state[side][e.Key], e)
}

// AdvanceWatermark moves the watermark of the given side forward and cleans
// state of the OPPOSITE side that can no longer be matched.
//
// An event on the opposite side is retained while an accepted event on this
// side could still match it. Because late events are accepted up to
// AllowedLateness behind the watermark, the cleanup horizon is shifted by the
// correction period as well.
//
// Watermarks are monotonic: advancing to a value not greater than the current
// watermark is a no-op.
func (j *Joiner) AdvanceWatermark(side Side, ts int64) {
	if ts <= j.watermark[side] {
		return
	}
	j.watermark[side] = ts
	j.cleanup(1 - side)
}

// cleanup drops events on the given side that can no longer be matched by any
// acceptable event from the opposite side.
//
// A left event l can only be matched by right events with
// r.Timestamp <= l.Timestamp+UpperBound, and such a right event is still
// accepted while r.Timestamp+AllowedLateness >= rightWatermark. Hence l is
// droppable once l.Timestamp+UpperBound+AllowedLateness < rightWatermark.
// The symmetric condition applies to right events using LowerBound.
func (j *Joiner) cleanup(side Side) {
	wm := j.watermark[1-side]
	if wm == math.MinInt64 {
		return
	}
	for key, evs := range j.state[side] {
		kept := evs[:0]
		for _, e := range evs {
			if j.matchHorizon(side, e.Timestamp) >= wm {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(j.state[side], key)
		} else {
			j.state[side][key] = kept
		}
	}
}

// matchHorizon returns the latest event time at which an event from the
// opposite side could still match an event of the given side and timestamp,
// including the correction period.
func (j *Joiner) matchHorizon(side Side, ts int64) int64 {
	if side == Left {
		// Latest matching right event time is ts+UpperBound.
		return ts + j.cfg.UpperBound + j.cfg.AllowedLateness
	}
	// Latest matching left event time is ts-LowerBound.
	return ts - j.cfg.LowerBound + j.cfg.AllowedLateness
}

// matches reports whether the pair satisfies the interval condition.
func (j *Joiner) matches(l, r *Event) bool {
	return r.Timestamp >= l.Timestamp+j.cfg.LowerBound &&
		r.Timestamp <= l.Timestamp+j.cfg.UpperBound
}
