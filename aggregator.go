package pixelagg

import "sort"

type pixelState struct {
	net   int
	first int64
	last  int64
}

type windowState struct {
	pixels map[PixelKey]*pixelState
}

// Aggregator collects pixel events into fixed windows. The watermark is
// the maximum event timestamp observed; a window closes once the
// watermark reaches windowEnd+allowedLateness.
type Aggregator struct {
	windowSize      int64
	allowedLateness int64
	watermark       int64
	hasWatermark    bool

	seen    map[string]struct{}
	windows map[int64]*windowState

	results     []WindowResult
	corrections []Correction
}

// New creates an Aggregator with the given fixed window size and allowed
// lateness bound (both in timestamp units). Window size must be positive.
func New(windowSize, allowedLateness int64) *Aggregator {
	if windowSize <= 0 {
		panic("pixelagg: window size must be positive")
	}
	if allowedLateness < 0 {
		panic("pixelagg: allowed lateness must be non-negative")
	}
	return &Aggregator{
		windowSize:      windowSize,
		allowedLateness: allowedLateness,
		seen:            make(map[string]struct{}),
		windows:         make(map[int64]*windowState),
	}
}

// floorDiv rounds toward negative infinity, so negative timestamps map
// to the correct window.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// WindowStart returns the inclusive start of the fixed window containing
// timestamp t. Windows are half-open: [start, start+windowSize).
func (a *Aggregator) WindowStart(t int64) int64 {
	return floorDiv(t, a.windowSize) * a.windowSize
}

// Add ingests one event. It returns false if the event was dropped as a
// duplicate. Events whose window is already closed are routed to the
// correction output instead of being aggregated.
func (a *Aggregator) Add(e Event) bool {
	if _, dup := a.seen[e.ID]; dup {
		return false
	}
	a.seen[e.ID] = struct{}{}

	if !a.hasWatermark || e.Timestamp > a.watermark {
		a.watermark = e.Timestamp
		a.hasWatermark = true
	}

	start := a.WindowStart(e.Timestamp)
	if a.isClosed(start) {
		a.corrections = append(a.corrections, Correction{Event: e, WindowStart: start})
		a.closeDue()
		return true
	}

	ws, ok := a.windows[start]
	if !ok {
		ws = &windowState{pixels: make(map[PixelKey]*pixelState)}
		a.windows[start] = ws
	}
	key := PixelKey{X: e.X, Y: e.Y}
	ps, ok := ws.pixels[key]
	if !ok {
		ps = &pixelState{first: e.Timestamp, last: e.Timestamp}
		ws.pixels[key] = ps
	}
	ps.net += e.Polarity
	if e.Timestamp < ps.first {
		ps.first = e.Timestamp
	}
	if e.Timestamp > ps.last {
		ps.last = e.Timestamp
	}

	a.closeDue()
	return true
}

// isClosed reports whether the window starting at start has been closed
// by the current watermark.
func (a *Aggregator) isClosed(start int64) bool {
	if !a.hasWatermark {
		return false
	}
	return a.watermark >= start+a.windowSize+a.allowedLateness
}

// closeDue emits every window whose end (plus allowed lateness) is at or
// behind the watermark.
func (a *Aggregator) closeDue() {
	if !a.hasWatermark {
		return
	}
	var due []int64
	for start := range a.windows {
		if a.isClosed(start) {
			due = append(due, start)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	for _, start := range due {
		a.emit(start)
	}
}

func (a *Aggregator) emit(start int64) {
	ws := a.windows[start]
	delete(a.windows, start)
	res := WindowResult{Start: start, End: start + a.windowSize}
	for key, ps := range ws.pixels {
		res.Pixels = append(res.Pixels, PixelSummary{
			Pixel:    key,
			NetCount: ps.net,
			First:    ps.first,
			Last:     ps.last,
		})
	}
	sort.Slice(res.Pixels, func(i, j int) bool {
		if res.Pixels[i].Pixel.X != res.Pixels[j].Pixel.X {
			return res.Pixels[i].Pixel.X < res.Pixels[j].Pixel.X
		}
		return res.Pixels[i].Pixel.Y < res.Pixels[j].Pixel.Y
	})
	a.results = append(a.results, res)
}

// Flush closes and emits all remaining open windows regardless of the
// watermark. Call it at end of input.
func (a *Aggregator) Flush() {
	var starts []int64
	for start := range a.windows {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	for _, start := range starts {
		a.emit(start)
	}
}

// Watermark returns the current watermark (maximum observed timestamp)
// and whether any event has been seen.
func (a *Aggregator) Watermark() (int64, bool) {
	return a.watermark, a.hasWatermark
}

// Results returns the closed window results in emission order.
func (a *Aggregator) Results() []WindowResult {
	out := make([]WindowResult, len(a.results))
	copy(out, a.results)
	return out
}

// Corrections returns events that arrived after their window was closed.
func (a *Aggregator) Corrections() []Correction {
	out := make([]Correction, len(a.corrections))
	copy(out, a.corrections)
	return out
}
