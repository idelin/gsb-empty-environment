// Package pixelagg aggregates pixel events (coordinates, polarity, time)
// into fixed time windows with bounded out-of-orderness driven by a
// watermark. Late events past the watermark are routed to a correction
// output. Duplicate events are removed by unique ID.
package pixelagg

// Event is a single pixel event.
type Event struct {
	// ID uniquely identifies the event; duplicates are dropped.
	ID string
	// X and Y are the pixel coordinates.
	X, Y int
	// Polarity is +1 (on) or -1 (off); any non-zero value is summed
	// into the net count.
	Polarity int
	// Timestamp is the event time; negative values are supported.
	Timestamp int64
}

// PixelKey identifies a pixel by its coordinates.
type PixelKey struct {
	X, Y int
}

// PixelSummary is the per-pixel aggregation result within one window.
// The time summary (First/Last) is retained even when NetCount cancels
// out to zero.
type PixelSummary struct {
	Pixel    PixelKey
	NetCount int
	First    int64 // earliest event timestamp in the window
	Last     int64 // latest event timestamp in the window
}

// WindowResult is the emitted output of a closed window.
type WindowResult struct {
	Start  int64 // inclusive window start
	End    int64 // exclusive window end
	Pixels []PixelSummary
}

// Correction describes an event that arrived after its window was
// already closed by the watermark.
type Correction struct {
	Event       Event
	WindowStart int64 // start of the window the event belongs to
}
