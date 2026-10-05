// Package pixelagg aggregates pixel events (coordinates, polarity, time)
// into fixed time windows. It tolerates bounded out-of-order input via a
// watermark; events arriving after their window has closed are routed to a
// correction output. Duplicate events are removed by unique ID.
package pixelagg

import (
	"errors"
	"sort"
)

// ErrNegativeTime is returned when an event carries a negative timestamp.
var ErrNegativeTime = errors.New("pixelagg: negative event time")

// ErrInvalidPolarity is returned when polarity is not +1 or -1.
var ErrInvalidPolarity = errors.New("pixelagg: polarity must be +1 or -1")

// Event is a single pixel event.
type Event struct {
	ID       string // unique identifier used for deduplication
	X, Y     int    // pixel coordinates
	Polarity int    // +1 (on) or -1 (off)
	Time     int64  // event timestamp, must be >= 0
}

// PixelSummary is the per-pixel aggregate within one window. Earliest and
// Latest are always kept, even when NetCount cancels to zero.
type PixelSummary struct {
	X, Y     int
	NetCount int   // sum of polarities
	Earliest int64 // minimum event time seen for this pixel
	Latest   int64 // maximum event time seen for this pixel
}

// Window is a closed fixed time window [Start, End) with its aggregates.
type Window struct {
	Start  int64
	End    int64
	Pixels []PixelSummary // sorted by (X, Y)
}

// Correction describes an event that arrived after its window closed
// (event time fell behind the watermark).
type Correction struct {
	Event       Event
	WindowStart int64 // window the event would have belonged to
	WindowEnd   int64
}

type pixelState struct {
	net      int
	earliest int64
	latest   int64
}

type windowState struct {
	pixels map[pixelKey]*pixelState
}

type pixelKey struct {
	x, y int
}

// Aggregator collects events into fixed windows.
type Aggregator struct {
	windowSize      int64
	allowedLateness int64

	maxTime int64
	seenAny bool

	open        map[int64]*windowState // window start -> state
	seenIDs     map[string]struct{}
	closed      []Window
	corrections []Correction
	duplicates  int
}

// New creates an Aggregator. windowSize is the fixed window length in time
// units; allowedLateness bounds out-of-orderness: the watermark is
// max(seen time) - allowedLateness, and a window closes once the watermark
// reaches its end.
func New(windowSize, allowedLateness int64) (*Aggregator, error) {
	if windowSize <= 0 {
		return nil, errors.New("pixelagg: window size must be positive")
	}
	if allowedLateness < 0 {
		return nil, errors.New("pixelagg: allowed lateness must be >= 0")
	}
	return &Aggregator{
		windowSize:      windowSize,
		allowedLateness: allowedLateness,
		open:            make(map[int64]*windowState),
		seenIDs:         make(map[string]struct{}),
	}, nil
}

// Add ingests one event. Duplicate IDs are silently dropped. Events whose
// window has already closed are appended to the correction output.
func (a *Aggregator) Add(ev Event) error {
	if ev.Time < 0 {
		return ErrNegativeTime
	}
	if ev.Polarity != 1 && ev.Polarity != -1 {
		return ErrInvalidPolarity
	}
	if ev.ID != "" {
		if _, dup := a.seenIDs[ev.ID]; dup {
			a.duplicates++
			return nil
		}
		a.seenIDs[ev.ID] = struct{}{}
	}

	if !a.seenAny || ev.Time > a.maxTime {
		a.maxTime = ev.Time
		a.seenAny = true
	}
	a.closeWindows()

	start := ev.Time - ev.Time%a.windowSize
	end := start + a.windowSize
	if a.watermark() >= end {
		a.corrections = append(a.corrections, Correction{
			Event:       ev,
			WindowStart: start,
			WindowEnd:   end,
		})
		return nil
	}

	ws := a.open[start]
	if ws == nil {
		ws = &windowState{pixels: make(map[pixelKey]*pixelState)}
		a.open[start] = ws
	}
	key := pixelKey{x: ev.X, y: ev.Y}
	ps := ws.pixels[key]
	if ps == nil {
		ps = &pixelState{earliest: ev.Time, latest: ev.Time}
		ws.pixels[key] = ps
	}
	ps.net += ev.Polarity
	if ev.Time < ps.earliest {
		ps.earliest = ev.Time
	}
	if ev.Time > ps.latest {
		ps.latest = ev.Time
	}
	return nil
}

// watermark returns the current watermark: the highest event time observed
// minus the allowed lateness. No watermark exists before the first event.
func (a *Aggregator) watermark() int64 {
	if !a.seenAny {
		return -1
	}
	return a.maxTime - a.allowedLateness
}

// closeWindows finalizes every open window whose end is at or below the
// watermark.
func (a *Aggregator) closeWindows() {
	wm := a.watermark()
	starts := make([]int64, 0, len(a.open))
	for start := range a.open {
		if start+a.windowSize <= wm {
			starts = append(starts, start)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	for _, start := range starts {
		a.closed = append(a.closed, buildWindow(start, a.windowSize, a.open[start]))
		delete(a.open, start)
	}
}

// Close flushes all remaining open windows, e.g. at end of stream.
func (a *Aggregator) Close() {
	starts := make([]int64, 0, len(a.open))
	for start := range a.open {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	for _, start := range starts {
		a.closed = append(a.closed, buildWindow(start, a.windowSize, a.open[start]))
		delete(a.open, start)
	}
}

// Windows returns the closed windows in order of window start.
func (a *Aggregator) Windows() []Window {
	out := make([]Window, len(a.closed))
	copy(out, a.closed)
	return out
}

// Corrections returns events that arrived after their window closed.
func (a *Aggregator) Corrections() []Correction {
	out := make([]Correction, len(a.corrections))
	copy(out, a.corrections)
	return out
}

// Duplicates returns the number of events dropped as duplicates.
func (a *Aggregator) Duplicates() int {
	return a.duplicates
}

func buildWindow(start, size int64, ws *windowState) Window {
	w := Window{Start: start, End: start + size}
	for key, ps := range ws.pixels {
		w.Pixels = append(w.Pixels, PixelSummary{
			X:        key.x,
			Y:        key.y,
			NetCount: ps.net,
			Earliest: ps.earliest,
			Latest:   ps.latest,
		})
	}
	sort.Slice(w.Pixels, func(i, j int) bool {
		if w.Pixels[i].X != w.Pixels[j].X {
			return w.Pixels[i].X < w.Pixels[j].X
		}
		return w.Pixels[i].Y < w.Pixels[j].Y
	})
	return w
}
