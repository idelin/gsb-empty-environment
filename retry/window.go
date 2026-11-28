package retry

import (
	"sync"
	"time"
)

// SlidingWindow counts events (normal request starts) inside a rolling time
// window. Old entries are evicted lazily on every read and write.
type SlidingWindow struct {
	mu      sync.Mutex
	window  time.Duration
	entries []time.Time
}

// NewSlidingWindow builds a rolling window of the given length.
func NewSlidingWindow(window time.Duration) *SlidingWindow {
	if window <= 0 {
		window = time.Second
	}
	return &SlidingWindow{window: window}
}

// Add records an event at now.
func (w *SlidingWindow) Add(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.evict(now)
	w.entries = append(w.entries, now)
}

// Count returns the number of events still inside the window at now.
func (w *SlidingWindow) Count(now time.Time) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.evict(now)
	return len(w.entries)
}

// Reset drops every recorded event.
func (w *SlidingWindow) Reset() {
	w.mu.Lock()
	w.entries = w.entries[:0]
	w.mu.Unlock()
}

func (w *SlidingWindow) evict(now time.Time) {
	cutoff := now.Add(-w.window)
	drop := 0
	for drop < len(w.entries) && !w.entries[drop].After(cutoff) {
		drop++
	}
	if drop > 0 {
		w.entries = append(w.entries[:0], w.entries[drop:]...)
	}
}
