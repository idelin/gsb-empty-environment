// Package slideq implements a sliding-window quantile aggregation over an
// event set that supports insert, revise and retract by ID.
//
// Windows are aligned to a slide grid, have a fixed size, and are
// left-open right-closed: (End-Size, End]. The watermark never regresses;
// a window's result is emitted once the watermark passes its end. Late
// corrections (revisions or retractions landing in an already emitted
// window) produce a retraction of the stale value followed by the new
// value. Every result is recomputed by rebuilding the sorted value set
// of the affected window from scratch.
package slideq

import "sort"

// Event is a single observation identified by ID.
type Event struct {
	ID        string
	Timestamp int64
	Value     float64
}

// Window is a left-open right-closed event-time interval (Start, End].
type Window struct {
	Start int64
	End   int64
}

// Contains reports whether t falls inside (w.Start, w.End].
func (w Window) Contains(t int64) bool {
	return w.Start < t && t <= w.End
}

// Emission is one output record. A retraction (Retract == true) carries
// the previously emitted value that is no longer valid; a plain emission
// carries the current value of the window.
type Emission struct {
	Window  Window
	Value   float64
	Retract bool
}

// Aggregator maintains the event set, the watermark and the set of
// windows whose results have been emitted.
type Aggregator struct {
	q       Fraction
	size    int64
	slide   int64
	events  map[string]Event
	emitted map[Window]float64

	watermark     int64
	watermarkInit bool
}

// NewAggregator builds an Aggregator. q must be within [0,1]; size and
// slide must be positive. It panics otherwise.
func NewAggregator(q Fraction, size, slide int64) *Aggregator {
	if q.Num < 0 || q.Num > q.Den {
		panic("slideq: q out of [0,1]")
	}
	if size <= 0 || slide <= 0 {
		panic("slideq: size and slide must be positive")
	}
	return &Aggregator{
		q:       q,
		size:    size,
		slide:   slide,
		events:  make(map[string]Event),
		emitted: make(map[Window]float64),
	}
}

// Insert adds a new event. It panics if the ID already exists.
func (a *Aggregator) Insert(e Event) []Emission {
	if _, ok := a.events[e.ID]; ok {
		panic("slideq: duplicate insert of " + e.ID)
	}
	return a.apply(e.ID, &e)
}

// Revise replaces the event with the same ID, or inserts it if absent.
func (a *Aggregator) Revise(e Event) []Emission {
	return a.apply(e.ID, &e)
}

// Retract removes the event with the given ID. Unknown IDs are a no-op.
func (a *Aggregator) Retract(id string) []Emission {
	return a.apply(id, nil)
}

// apply mutates the event set and reconciles every already emitted
// window that overlaps the old or new position of the event.
func (a *Aggregator) apply(id string, next *Event) []Emission {
	affected := make(map[Window]struct{})
	if old, ok := a.events[id]; ok {
		for _, w := range a.windowsContaining(old.Timestamp) {
			affected[w] = struct{}{}
		}
	}
	if next == nil {
		delete(a.events, id)
	} else {
		a.events[id] = *next
		for _, w := range a.windowsContaining(next.Timestamp) {
			affected[w] = struct{}{}
		}
	}

	var out []Emission
	for w := range affected {
		if !a.watermarkInit || w.End > a.watermark {
			continue // window not complete yet; nothing to correct
		}
		prev, wasEmitted := a.emitted[w]
		value, nonEmpty := a.compute(w)
		switch {
		case wasEmitted && !nonEmpty:
			out = append(out, Emission{Window: w, Value: prev, Retract: true})
			delete(a.emitted, w)
		case wasEmitted && value != prev:
			out = append(out,
				Emission{Window: w, Value: prev, Retract: true},
				Emission{Window: w, Value: value},
			)
			a.emitted[w] = value
		case !wasEmitted && nonEmpty:
			// The window is already behind the watermark but has no
			// live output (e.g. it was retracted empty earlier):
			// emit its current value immediately.
			out = append(out, Emission{Window: w, Value: value})
			a.emitted[w] = value
		}
	}
	return sortEmissions(out)
}

// AdvanceWatermark moves the watermark forward and emits the result of
// every window whose end is now at or behind the watermark. A timestamp
// at or behind the current watermark is ignored: the watermark never
// regresses.
func (a *Aggregator) AdvanceWatermark(ts int64) []Emission {
	if a.watermarkInit && ts <= a.watermark {
		return nil
	}
	a.watermark = ts
	a.watermarkInit = true

	seen := make(map[Window]struct{})
	for _, e := range a.events {
		for _, w := range a.windowsContaining(e.Timestamp) {
			if w.End <= ts {
				seen[w] = struct{}{}
			}
		}
	}
	var out []Emission
	for w := range seen {
		if _, ok := a.emitted[w]; ok {
			continue
		}
		if value, nonEmpty := a.compute(w); nonEmpty {
			a.emitted[w] = value
			out = append(out, Emission{Window: w, Value: value})
		}
	}
	return sortEmissions(out)
}

// Watermark reports the current watermark and whether it was ever set.
func (a *Aggregator) Watermark() (int64, bool) {
	return a.watermark, a.watermarkInit
}

// compute rebuilds the sorted value set of w from scratch and returns
// its quantile. nonEmpty is false when the window holds no events.
func (a *Aggregator) compute(w Window) (value float64, nonEmpty bool) {
	var values []float64
	for _, e := range a.events {
		if w.Contains(e.Timestamp) {
			values = append(values, e.Value)
		}
	}
	if len(values) == 0 {
		return 0, false
	}
	return Quantile(values, a.q), true
}

// windowsContaining lists every grid-aligned window that contains t,
// i.e. all windows (k*slide-size, k*slide] with Start < t <= End.
func (a *Aggregator) windowsContaining(t int64) []Window {
	kMin := ceilDiv(t, a.slide)           // smallest k with k*slide >= t
	kMax := floorDiv(t+a.size-1, a.slide) // largest k with k*slide < t+size
	windows := make([]Window, 0, kMax-kMin+1)
	for k := kMin; k <= kMax; k++ {
		end := k * a.slide
		windows = append(windows, Window{Start: end - a.size, End: end})
	}
	return windows
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func ceilDiv(a, b int64) int64 {
	return -floorDiv(-a, b)
}

// sortEmissions orders output deterministically: by window end, then
// start, with the retraction of a window before its new value.
func sortEmissions(in []Emission) []Emission {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.Window.End != b.Window.End {
			return a.Window.End < b.Window.End
		}
		if a.Window.Start != b.Window.Start {
			return a.Window.Start < b.Window.Start
		}
		return a.Retract && !b.Retract
	})
	return in
}
