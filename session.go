// Package sessionwin implements a revocable session-window merger.
//
// Events are merged into session windows whenever the gap between
// adjacent events is <= gap. Late events may bridge two previously
// independent windows; retractions may split a window back apart.
// Every mutation produces a changelog of Retract/Upsert records so
// downstream consumers can undo and redo window results.
//
// A watermark bounds how far back state is kept: once the watermark
// advances past an event's timestamp by more than maxLateness, the
// event is discarded and its (already final) window is sealed without
// emitting further changes.
package sessionwin

import "sort"

// Window is a closed session interval [Start, End] over event timestamps.
type Window struct {
	Start int64
	End   int64
}

// ChangeKind distinguishes changelog record types.
type ChangeKind int

const (
	// Retract undoes a previously emitted window.
	Retract ChangeKind = iota
	// Upsert publishes a new or updated window.
	Upsert
)

func (k ChangeKind) String() string {
	if k == Retract {
		return "RETRACT"
	}
	return "UPSERT"
}

// Change is a single changelog record.
type Change struct {
	Kind   ChangeKind
	Window Window
}

// Merger holds the session-window state.
type Merger struct {
	gap         int64
	maxLateness int64
	watermark   int64
	events      map[int64]int // timestamp -> multiplicity
	windows     []Window      // currently active windows, sorted by Start
}

// NewMerger creates a Merger with the given session gap and maximum
// allowed lateness (both in event-time units).
func NewMerger(gap, maxLateness int64) *Merger {
	return &Merger{
		gap:         gap,
		maxLateness: maxLateness,
		watermark:   -1 << 62,
		events:      make(map[int64]int),
	}
}

// Watermark returns the current watermark.
func (m *Merger) Watermark() int64 { return m.watermark }

// Windows returns a copy of the currently active windows.
func (m *Merger) Windows() []Window {
	out := make([]Window, len(m.windows))
	copy(out, m.windows)
	return out
}

// Add ingests an event at timestamp t and returns the changelog.
// Events older than watermark-maxLateness are dropped silently.
func (m *Merger) Add(t int64) []Change {
	if t+m.maxLateness <= m.watermark {
		return nil // beyond allowed lateness: discarded
	}
	m.events[t]++
	return m.recompute(-1 << 62)
}

// Remove retracts one occurrence of an event at timestamp t and
// returns the changelog. Unknown timestamps are a no-op.
func (m *Merger) Remove(t int64) []Change {
	if m.events[t] == 0 {
		return nil
	}
	m.events[t]--
	if m.events[t] == 0 {
		delete(m.events, t)
	}
	return m.recompute(-1 << 62)
}

// AdvanceWatermark moves the watermark forward to wm and discards
// state older than wm-maxLateness. Windows that end at or below the
// cutoff are final: they are sealed without emitting retractions.
// Returns the changelog for any still-active windows that changed.
func (m *Merger) AdvanceWatermark(wm int64) []Change {
	if wm <= m.watermark {
		return nil
	}
	m.watermark = wm
	cutoff := wm - m.maxLateness
	for t := range m.events {
		if t <= cutoff {
			delete(m.events, t)
		}
	}
	return m.recompute(cutoff)
}

// recompute rebuilds the window list from the live event set and
// diffs it against the previous list. Removed windows ending at or
// below cutoff are sealed and produce no retraction.
func (m *Merger) recompute(cutoff int64) []Change {
	next := computeWindows(m.events, m.gap)
	var changes []Change
	for _, w := range m.windows {
		if !contains(next, w) && w.End > cutoff {
			changes = append(changes, Change{Kind: Retract, Window: w})
		}
	}
	for _, w := range next {
		if !contains(m.windows, w) {
			changes = append(changes, Change{Kind: Upsert, Window: w})
		}
	}
	m.windows = next
	return changes
}

// computeWindows groups sorted timestamps into sessions where
// adjacent events are at most gap apart.
func computeWindows(events map[int64]int, gap int64) []Window {
	ts := make([]int64, 0, len(events))
	for t := range events {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })

	var windows []Window
	for _, t := range ts {
		if n := len(windows); n > 0 && t-windows[n-1].End <= gap {
			windows[n-1].End = t
		} else {
			windows = append(windows, Window{Start: t, End: t})
		}
	}
	return windows
}

func contains(ws []Window, w Window) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}
