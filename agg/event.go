// Package agg implements a versioned, fixed-bucket telemetry aggregation
// store. Events are aggregated into buckets of a fixed width with
// min/max/sum/count and first/last values. Interval corrections and
// deletions are versioned so older snapshots remain queryable.
package agg

import "math"

// Event is a single telemetry sample. ID uniquely identifies an event among
// active events and is used to deduplicate overlapping batches.
type Event struct {
	ID    string  `json:"id"`
	TS    int64   `json:"ts"`
	Value float64 `json:"value"`
}

// lessEvent orders events by event time, breaking ties by ID. First/last
// values are defined by this order.
func lessEvent(a, b Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.ID < b.ID
}

// minValue returns the event with the smaller value; ties break by (TS, ID)
// so results are deterministic.
func minValue(a, b Event) Event {
	if a.Value != b.Value {
		if a.Value < b.Value {
			return a
		}
		return b
	}
	if lessEvent(a, b) {
		return a
	}
	return b
}

// maxValue returns the event with the larger value; ties break by (TS, ID)
// so peak evidence is deterministic.
func maxValue(a, b Event) Event {
	if a.Value != b.Value {
		if a.Value > b.Value {
			return a
		}
		return b
	}
	if lessEvent(a, b) {
		return a
	}
	return b
}

// Summary is the aggregate of a set of events. Present distinguishes an
// empty set (a gap) from a set whose values happen to be zero.
type Summary struct {
	Present bool
	Count   int
	Sum     float64
	Min     Event // smallest value; peak evidence counterpart
	Max     Event // largest value; this is the peak evidence
	First   Event // earliest by (TS, ID)
	Last    Event // latest by (TS, ID)
}

// Mean is Sum/Count. It is never an average of per-bucket means. It returns
// NaN for an empty summary.
func (s Summary) Mean() float64 {
	if s.Count == 0 {
		return math.NaN()
	}
	return s.Sum / float64(s.Count)
}

// combine merges two summaries without double counting: it assumes a and b
// cover disjoint event sets.
func combine(a, b Summary) Summary {
	if !a.Present {
		return b
	}
	if !b.Present {
		return a
	}
	out := Summary{
		Present: true,
		Count:   a.Count + b.Count,
		Sum:     a.Sum + b.Sum,
		Min:     minValue(a.Min, b.Min),
		Max:     maxValue(a.Max, b.Max),
	}
	if lessEvent(a.First, b.First) {
		out.First = a.First
	} else {
		out.First = b.First
	}
	if lessEvent(a.Last, b.Last) {
		out.Last = b.Last
	} else {
		out.Last = a.Last
	}
	return out
}

// summarizeEvents aggregates an arbitrary (unordered) set of events.
func summarizeEvents(events []Event) Summary {
	if len(events) == 0 {
		return Summary{}
	}
	s := Summary{
		Present: true,
		Count:   len(events),
		Sum:     events[0].Value,
		Min:     events[0],
		Max:     events[0],
		First:   events[0],
		Last:    events[0],
	}
	for _, e := range events[1:] {
		s.Sum += e.Value
		s.Min = minValue(s.Min, e)
		s.Max = maxValue(s.Max, e)
		if lessEvent(e, s.First) {
			s.First = e
		}
		if lessEvent(s.Last, e) {
			s.Last = e
		}
	}
	return s
}
