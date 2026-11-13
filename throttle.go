package netsec

import "time"

// Default throttling policy: more than 10 RAKP failures from one source
// within 60 seconds drops its subsequent session requests.
const (
	DefaultThrottleWindow      = 60 * time.Second
	DefaultThrottleMaxFailures = 10
)

// Throttle tracks per-source RAKP failures inside a sliding window.
type Throttle struct {
	window     time.Duration
	maxFailure int
	failures   map[string][]time.Time
}

func NewThrottle(window time.Duration, maxFailures int) *Throttle {
	return &Throttle{
		window:     window,
		maxFailure: maxFailures,
		failures:   make(map[string][]time.Time),
	}
}

// Dropped reports whether the source has exceeded its failure budget and
// its session requests must be discarded.
func (t *Throttle) Dropped(source string, now time.Time) bool {
	return len(t.prune(source, now)) > t.maxFailure
}

// RecordFailure notes a failed RAKP attempt from source at now.
func (t *Throttle) RecordFailure(source string, now time.Time) {
	t.failures[source] = append(t.prune(source, now), now)
}

func (t *Throttle) prune(source string, now time.Time) []time.Time {
	kept := t.failures[source][:0]
	for _, ts := range t.failures[source] {
		if now.Sub(ts) < t.window {
			kept = append(kept, ts)
		}
	}
	t.failures[source] = kept
	return kept
}
