package alertseg

import (
	"sort"
	"time"
)

// Monitor merges raw telemetry state changes into segments and decides when
// notifications are sent. All time is injected (event timestamps, Tick and
// SetSuppressed arguments), so behavior is fully deterministic and testable.
//
// Semantics:
//   - Consecutive identical states merge into one segment; only the first
//     FIRING event of a segment notifies.
//   - While a firing segment persists, Tick emits REPEAT every repeatInterval.
//   - Suppression never changes the tracked real state; it only holds
//     notifications.
//   - A fault that is still firing when suppression lifts is notified then
//     (reason "suppression-lifted").
//   - A fault that recovered during suppression is never backfilled.
type Monitor struct {
	repeatInterval time.Duration
	suppressed     bool
	sources        map[string]*sourceState
	notifications  []Notification
	closed         []Segment
}

type sourceState struct {
	seg        Segment
	hasSegment bool
	notified   bool // current firing segment has been notified at least once
	lastNotify time.Time
}

func NewMonitor(repeatInterval time.Duration) *Monitor {
	return &Monitor{
		repeatInterval: repeatInterval,
		sources:        make(map[string]*sourceState),
	}
}

// Apply folds one telemetry event into the monitor.
func (m *Monitor) Apply(ev TelemetryEvent) {
	s := m.source(ev.Source)
	if s.hasSegment && s.seg.State == ev.State {
		s.seg.Events++
		return
	}

	prevNotified := s.notified
	var closedSeg Segment
	if s.hasSegment {
		s.seg.End = ev.Time
		s.seg.Open = false
		closedSeg = s.seg
		m.closed = append(m.closed, s.seg)
	}

	s.seg = Segment{Source: ev.Source, State: ev.State, Start: ev.Time, Events: 1, Open: true}
	s.hasSegment = true
	s.notified = false

	switch ev.State {
	case StateFiring:
		if !m.suppressed {
			m.emit(s, s.seg, ev.Time, NotifyFiring, "state-change")
		}
	case StateOK:
		// Only resolve a fault the outside world actually knows about,
		// and never while suppressed.
		if prevNotified && !m.suppressed {
			m.emit(s, closedSeg, ev.Time, NotifyResolved, "state-change")
		}
	}
}

// Tick advances time, emitting due repeat notifications for ongoing faults.
func (m *Monitor) Tick(t time.Time) {
	if m.suppressed || m.repeatInterval <= 0 {
		return
	}
	for _, name := range m.sortedSources() {
		s := m.sources[name]
		if s.hasSegment && s.seg.State == StateFiring && s.notified &&
			!t.Before(s.lastNotify.Add(m.repeatInterval)) {
			m.emit(s, s.seg, t, NotifyRepeat, "repeat-interval")
		}
	}
}

// SetSuppressed toggles suppression at time t. Lifting suppression backfills
// a notification for any fault that began (and is still firing) while
// suppressed. Faults that already recovered stay silent.
func (m *Monitor) SetSuppressed(suppressed bool, t time.Time) {
	if m.suppressed == suppressed {
		return
	}
	m.suppressed = suppressed
	if suppressed {
		return
	}
	for _, name := range m.sortedSources() {
		s := m.sources[name]
		if s.hasSegment && s.seg.State == StateFiring && !s.notified {
			m.emit(s, s.seg, t, NotifyFiring, "suppression-lifted")
		}
	}
}

// Suppressed reports whether notifications are currently held.
func (m *Monitor) Suppressed() bool { return m.suppressed }

// Notifications returns the notifications emitted so far, in order.
func (m *Monitor) Notifications() []Notification {
	return append([]Notification(nil), m.notifications...)
}

// Segments returns all closed segments followed by the still-open ones.
func (m *Monitor) Segments() []Segment {
	out := append([]Segment(nil), m.closed...)
	for _, name := range m.sortedSources() {
		if s := m.sources[name]; s.hasSegment {
			out = append(out, s.seg)
		}
	}
	return out
}

func (m *Monitor) emit(s *sourceState, seg Segment, t time.Time, kind NotifyKind, reason string) {
	s.notified = true
	s.lastNotify = t
	m.notifications = append(m.notifications, Notification{
		Time:    t,
		Source:  seg.Source,
		Kind:    kind,
		Reason:  reason,
		Segment: seg,
	})
}

func (m *Monitor) source(name string) *sourceState {
	s, ok := m.sources[name]
	if !ok {
		s = &sourceState{}
		m.sources[name] = s
	}
	return s
}

func (m *Monitor) sortedSources() []string {
	names := make([]string, 0, len(m.sources))
	for name := range m.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
