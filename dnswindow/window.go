// Package dnswindow provides a fixed, half-open tumbling window aggregator for
// DNS query events observed by a network probe.
//
// Events are grouped by (client, registered domain) and aggregated into
// explainable features: the label-length distribution, the number of distinct
// labels, the NXDOMAIN ratio and the Shannon byte entropy of the queried
// names. Windows are closed by an advancing watermark, out-of-order events are
// handled correctly, duplicate event IDs are counted at most once and an event
// may be corrected (replaced) before its window closes; corrections withdraw
// the contribution of the previous version of the event.
package dnswindow

import (
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Outcome reports how an event was handled by Manager.Submit.
type Outcome int

const (
	// OutcomeAccepted means the event was added to an open window.
	OutcomeAccepted Outcome = iota
	// OutcomeDuplicate means the event ID was already present; nothing changed.
	OutcomeDuplicate
	// OutcomeCorrected means a previous event with the same ID was replaced and
	// its contribution withdrawn.
	OutcomeCorrected
)

// String returns a human-readable name of the outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeAccepted:
		return "accepted"
	case OutcomeDuplicate:
		return "duplicate"
	case OutcomeCorrected:
		return "corrected"
	default:
		return "unknown"
	}
}

var (
	// ErrInvalidEvent is returned when an event has an empty ID or a zero time.
	ErrInvalidEvent = errors.New("dnswindow: invalid event: empty ID or zero time")
	// ErrLate is returned when an event belongs to a window that the watermark
	// has already closed.
	ErrLate = errors.New("dnswindow: event is late for a sealed window")
	// ErrSealed is returned when correcting or reusing an event ID whose window
	// has already been sealed.
	ErrSealed = errors.New("dnswindow: event belongs to a sealed window")
)

// Event is one DNS query observation.
//
// ID is the deduplication key assigned by the caller. Client and Registered
// domain form the grouping key. QueriedName is the raw queried domain; a
// single trailing dot is ignored when labels and bytes are examined.
// NXDOMAIN marks a query that returned the NXDOMAIN rcode.
type Event struct {
	ID               string
	Client           string
	RegisteredDomain string
	QueriedName      string
	NXDOMAIN         bool
	Time             time.Time
}

// equal reports whether two versions of the same event ID carry identical
// payloads, in which case a correction is a no-op duplicate.
func (e Event) equal(other Event) bool {
	return e.ID == other.ID &&
		e.Client == other.Client &&
		e.RegisteredDomain == other.RegisteredDomain &&
		e.QueriedName == other.QueriedName &&
		e.NXDOMAIN == other.NXDOMAIN &&
		e.Time.Equal(other.Time)
}

// GroupFeatures holds the exact aggregated features for one
// (client, registered domain) group inside one window.
type GroupFeatures struct {
	Client           string
	RegisteredDomain string

	// TotalEvents is the number of deduplicated events in the group.
	TotalEvents int64
	// NXDOMAINEvents counts events whose query returned NXDOMAIN.
	NXDOMAINEvents int64
	// NXDOMAINRatio is NXDOMAINEvents / TotalEvents and 0 for an empty group.
	NXDOMAINRatio float64

	// LabelLengthHist maps a label length to the number of labels of that
	// length observed across all queried names in the group.
	LabelLengthHist map[int]int64
	// DistinctLabels is the number of distinct label strings observed.
	DistinctLabels int64

	// TotalNameBytes is the number of bytes counted toward ByteEntropy.
	TotalNameBytes int64
	// ByteFrequency holds the occurrence count of every byte value appearing
	// in the queried names; absent values have count zero.
	ByteFrequency [256]int64
	// ByteEntropy is the Shannon entropy (base 2, bits per byte) of the
	// queried-name bytes. It is 0 when no bytes were observed.
	ByteEntropy float64
}

// WindowResult is the frozen feature set emitted when a window closes.
type WindowResult struct {
	// Start is the inclusive start of the half-open interval [Start, End).
	Start time.Time
	// End is the exclusive end of the interval and equals Start + window size.
	End time.Time
	// Groups is sorted by (Client, RegisteredDomain).
	Groups []GroupFeatures
}

// Manager aggregates events into fixed half-open windows grouped by
// (client, registered domain). It is safe for concurrent use.
//
// Event IDs are deduplicated for the whole lifetime of the Manager: once an ID
// has been sealed into an emitted window it is tombstoned and any later
// submission carrying it returns ErrSealed, so a probe event can never be
// counted twice. The tombstone retains one empty struct per distinct ID, which
// is the intrinsic memory cost of global deduplication.
type Manager struct {
	mu        sync.Mutex
	size      time.Duration
	watermark time.Time
	windows   map[time.Time]*window
	// index maps an event ID to the window holding its current version.
	index map[string]time.Time
	// sealedIDs tombstones IDs already emitted in a sealed window.
	sealedIDs map[string]struct{}
}

// NewManager creates a Manager whose windows have the given fixed size.
// Size must be positive; windows are aligned to the Unix epoch in UTC.
func NewManager(size time.Duration) (*Manager, error) {
	if size <= 0 {
		return nil, errors.New("dnswindow: window size must be positive")
	}
	return &Manager{
		size:      size,
		windows:   make(map[time.Time]*window),
		index:     make(map[string]time.Time),
		sealedIDs: make(map[string]struct{}),
	}, nil
}

// Submit adds a new event or corrects an existing one with the same ID.
//
// A first occurrence is added to the half-open window containing its time.
// An ID already present in an open window is replaced: the old version's
// contribution is fully withdrawn (including cross-window and cross-group
// moves) before the new version is added, returning OutcomeCorrected. An
// identical re-submission returns OutcomeDuplicate and changes nothing.
//
// Events falling at or before the watermark are rejected with ErrLate;
// operations on IDs already sealed in a closed window return ErrSealed.
func (m *Manager) Submit(event Event) (Outcome, error) {
	if event.ID == "" || event.Time.IsZero() {
		return 0, ErrInvalidEvent
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, sealed := m.sealedIDs[event.ID]; sealed {
		return 0, ErrSealed
	}

	if existingStart, ok := m.index[event.ID]; ok {
		oldWindow := m.windows[existingStart]
		oldEvent := oldWindow.events[event.ID]
		if oldEvent.equal(event) {
			return OutcomeDuplicate, nil
		}
		if !event.Time.After(m.watermark) {
			return 0, ErrLate
		}

		m.removeEvent(oldWindow, event.ID)
		m.insertEvent(event)
		return OutcomeCorrected, nil
	}

	if !event.Time.After(m.watermark) {
		return 0, ErrLate
	}
	m.insertEvent(event)
	return OutcomeAccepted, nil
}

// insertEvent stores the event in its window, creating the window if needed.
// Callers must hold m.mu and must have validated the event.
func (m *Manager) insertEvent(event Event) {
	start := truncateWindowStart(event.Time, m.size)
	win, ok := m.windows[start]
	if !ok {
		win = newWindow(start, m.size)
		m.windows[start] = win
	}
	win.add(event)
	m.index[event.ID] = start
}

// removeEvent deletes an event from its window, withdrawing its contribution.
// Empty groups and windows are pruned. Callers must hold m.mu.
func (m *Manager) removeEvent(win *window, id string) {
	win.remove(id)
	delete(m.index, id)
	if len(win.events) == 0 {
		delete(m.windows, win.start)
	}
}

// Advance moves the watermark to t. Windows whose exclusive end is at or
// before the new watermark are sealed: their frozen features are returned in
// chronological order and their state is released. Calls that do not move the
// watermark forward (including the first call establishing it) return nil.
func (m *Manager) Advance(t time.Time) []WindowResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !t.After(m.watermark) {
		return nil
	}
	m.watermark = t

	var starts []time.Time
	for start, win := range m.windows {
		if !win.end.After(t) { // win.end <= watermark: [start, end) is sealed
			starts = append(starts, start)
		}
	}
	if len(starts) == 0 {
		return nil
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	results := make([]WindowResult, 0, len(starts))
	for _, start := range starts {
		win := m.windows[start]
		results = append(results, win.snapshot())
		delete(m.windows, start)
		for id := range win.events {
			delete(m.index, id)
			m.sealedIDs[id] = struct{}{}
		}
	}
	return results
}

// Watermark returns the current watermark. No event with a time at or before
// the watermark can be admitted.
func (m *Manager) Watermark() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.watermark
}

// truncateWindowStart floors t to the start of the fixed-size window
// containing it. Windows are aligned to the Unix epoch in UTC, so division
// works for all non-negative times and yields deterministic half-open
// intervals.
func truncateWindowStart(t time.Time, size time.Duration) time.Time {
	utc := t.UTC()
	idx := utc.UnixNano() / int64(size)
	return time.Unix(0, idx*int64(size)).UTC()
}

// labelsOf splits a queried name into its DNS labels, ignoring a single
// trailing dot. The empty name (or ".") yields no labels.
func labelsOf(name string) []string {
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

// shannonEntropy computes the base-2 Shannon entropy of the byte distribution
// of data. It returns 0 for empty input.
func shannonEntropy(freq [256]int64, total int64) float64 {
	if total == 0 {
		return 0
	}
	var entropy float64
	totalFloat := float64(total)
	for _, count := range freq {
		if count == 0 {
			continue
		}
		p := float64(count) / totalFloat
		entropy -= p * math.Log2(p)
	}
	return entropy
}
