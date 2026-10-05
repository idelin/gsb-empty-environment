package sliding

import (
	"errors"
	"fmt"
	"math"
)

// OutputKind distinguishes emissions from retractions.
type OutputKind int

const (
	// Emit publishes the quantile of a window.
	Emit OutputKind = iota
	// Retract withdraws a previously emitted quantile. It is always
	// followed by an Emit carrying the corrected value, unless the
	// window became empty.
	Retract
)

// Output is one row of the result stream.
type Output struct {
	Kind        OutputKind
	WindowStart int64 // exclusive
	WindowEnd   int64 // inclusive
	Value       float64
}

func (o Output) String() string {
	kind := "EMIT"
	if o.Kind == Retract {
		kind = "RETRACT"
	}
	return fmt.Sprintf("%s (%d, %d] %v", kind, o.WindowStart, o.WindowEnd, o.Value)
}

// Engine computes a sliding-window quantile over an event-time stream.
//
// Windows are (k*slide, size+k*slide] for k = 0, 1, 2, ...: left-open,
// right-closed. A window's result is emitted once the watermark passes its
// end. The watermark never retreats. Events are retained indefinitely so
// that arbitrarily late revisions/retractions still correct past outputs.
type Engine struct {
	store     *Store
	size      int64
	slide     int64
	q         Fraction
	watermark int64
	closed    map[int64]bool    // window end -> watermark passed it
	visible   map[int64]float64 // window end -> last emitted value
}

// NewEngine builds an Engine with the given window size, slide and quantile.
func NewEngine(size, slide int64, q Fraction) (*Engine, error) {
	if size <= 0 {
		return nil, errors.New("sliding: window size must be positive")
	}
	if slide <= 0 {
		return nil, errors.New("sliding: slide must be positive")
	}
	return &Engine{
		store:     NewStore(),
		size:      size,
		slide:     slide,
		q:         q,
		watermark: math.MinInt64,
		closed:    make(map[int64]bool),
		visible:   make(map[int64]float64),
	}, nil
}

// Watermark returns the current watermark.
func (e *Engine) Watermark() int64 {
	return e.watermark
}

// Upsert inserts or revises an event by ID and returns any corrections to
// already-closed windows. A revision that moves the event across windows
// corrects both the old and the new position.
func (e *Engine) Upsert(ev Event) []Output {
	old, replaced := e.store.Upsert(ev)
	var outs []Output
	if replaced && old.Time != ev.Time {
		outs = append(outs, e.refresh(old.Time)...)
	}
	return append(outs, e.refresh(ev.Time)...)
}

// Retract removes the event with the given ID and returns any corrections.
func (e *Engine) Retract(id string) []Output {
	old, ok := e.store.Retract(id)
	if !ok {
		return nil
	}
	return e.refresh(old.Time)
}

// AdvanceWatermark moves the watermark forward and emits results for every
// window whose end it passes. A watermark at or below the current one is
// ignored: the watermark never retreats.
func (e *Engine) AdvanceWatermark(t int64) []Output {
	if t <= e.watermark {
		return nil
	}
	e.watermark = t
	var outs []Output
	for end := e.size; end <= t; end += e.slide {
		if e.closed[end] {
			continue
		}
		e.closed[end] = true
		if v, ok := e.compute(end); ok {
			e.visible[end] = v
			outs = append(outs, Output{Kind: Emit, WindowStart: end - e.size, WindowEnd: end, Value: v})
		}
	}
	return outs
}

// refresh recomputes every closed window containing event time t.
func (e *Engine) refresh(t int64) []Output {
	var outs []Output
	// Windows containing t satisfy end-size < t <= end, i.e. t <= end < t+size.
	for end := e.firstEndAtOrAfter(t); end < t+e.size; end += e.slide {
		if !e.closed[end] {
			continue
		}
		outs = append(outs, e.recompute(end)...)
	}
	return outs
}

// firstEndAtOrAfter returns the smallest window end >= t.
func (e *Engine) firstEndAtOrAfter(t int64) int64 {
	d := t - e.size
	var k int64
	if d > 0 {
		k = (d + e.slide - 1) / e.slide
	}
	return e.size + k*e.slide
}

// recompute diffs the current contents of a closed window against the last
// emitted value and produces the minimal correction.
func (e *Engine) recompute(end int64) []Output {
	start := end - e.size
	v, ok := e.compute(end)
	old, had := e.visible[end]
	switch {
	case had && ok && old == v:
		return nil
	case had && ok:
		e.visible[end] = v
		return []Output{
			{Kind: Retract, WindowStart: start, WindowEnd: end, Value: old},
			{Kind: Emit, WindowStart: start, WindowEnd: end, Value: v},
		}
	case had && !ok:
		delete(e.visible, end)
		return []Output{{Kind: Retract, WindowStart: start, WindowEnd: end, Value: old}}
	case !had && ok:
		e.visible[end] = v
		return []Output{{Kind: Emit, WindowStart: start, WindowEnd: end, Value: v}}
	default:
		return nil
	}
}

// compute rebuilds the sorted set of the window and evaluates the quantile.
func (e *Engine) compute(end int64) (float64, bool) {
	return Quantile(e.store.windowValues(end-e.size, end), e.q)
}
