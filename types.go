package alertseg

import "time"

// State is the health state reported by a telemetry event.
type State string

const (
	StateOK     State = "OK"
	StateFiring State = "FIRING"
)

// TelemetryEvent is a single raw state report from a source.
type TelemetryEvent struct {
	Time   time.Time
	Source string
	State  State
}

// Segment is a run of consecutive identical states merged into one span.
type Segment struct {
	Source string
	State  State
	Start  time.Time
	End    time.Time // only meaningful when Open is false
	Events int       // number of raw events merged into this segment
	Open   bool      // true while the segment is still the current state
}

// NotifyKind classifies a notification.
type NotifyKind string

const (
	NotifyFiring   NotifyKind = "FIRING"
	NotifyRepeat   NotifyKind = "REPEAT"
	NotifyResolved NotifyKind = "RESOLVED"
)

// Notification is an outbound alert emitted by the monitor.
type Notification struct {
	Time    time.Time
	Source  string
	Kind    NotifyKind
	Reason  string // state-change | repeat-interval | suppression-lifted
	Segment Segment
}
