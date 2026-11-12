package recovmon

import (
	"errors"
	"fmt"
)

// ErrConfig means the raw interval or authenticated coverage is invalid.
var ErrConfig = errors.New("recovmon: invalid monitor configuration")

type fieldState struct {
	name      string
	path      string
	declared  Interval
	actualEnd int64
	depth     int
	open      bool
	truncated bool
}

// Monitor watches a parser event stream and proves whether error recovery
// stays within unauthenticated, unconsumed field boundaries.
//
// All methods are safe for sequential single-goroutine use by the test host.
type Monitor struct {
	raw       Interval
	auth      []Interval
	consumed  []Interval
	discarded []Interval
	fields    []*fieldState
	stack     []*fieldState

	violations    []Violation
	lastError     bool
	recoveryCount int
	eventCount    int
}

// NewMonitor builds a monitor for raw and the authenticated coverage set.
// Authenticated intervals must be valid and fully inside raw; they are
// normalized into a merged, disjoint list.
func NewMonitor(raw Interval, authenticated []Interval) (*Monitor, error) {
	if !raw.Valid() {
		return nil, fmt.Errorf("%w: raw interval %v invalid", ErrConfig, raw)
	}
	m := &Monitor{raw: raw}
	for _, cover := range authenticated {
		if !cover.Valid() {
			return nil, fmt.Errorf("%w: authenticated interval %v invalid", ErrConfig, cover)
		}
		if !m.coversRaw(cover) {
			return nil, fmt.Errorf("%w: authenticated interval %v outside raw %v", ErrConfig, cover, raw)
		}
		m.auth = addMerged(m.auth, cover)
	}
	return m, nil
}

// Apply feeds one parser event and returns the first violation it causes,
// or nil when the event is legal. A rejected event never mutates state, so
// the monitor keeps observing the remainder of the stream.
func (m *Monitor) Apply(e Event) *Violation {
	index := m.eventCount
	m.eventCount++

	if e.Kind < EventBegin || e.Kind > EventRecovery {
		return m.failPtr(index, ViolationInvalidSpan, e, "unknown event kind")
	}
	if !e.Span.Valid() {
		return m.failPtr(index, ViolationInvalidSpan, e, "event span is empty or negative")
	}
	if !m.coversRaw(e.Span) {
		return m.failPtr(index, ViolationSpanOutOfRange, e,
			fmt.Sprintf("span %v leaves raw interval %v", e.Span, m.raw))
	}

	switch e.Kind {
	case EventBegin:
		return m.applyBegin(index, e)
	case EventEnd:
		return m.applyEnd(index, e)
	case EventConsume:
		return m.applyConsume(index, e)
	case EventError:
		m.lastError = true
		return nil
	case EventRecovery:
		return m.applyRecovery(index, e)
	default:
		return m.failPtr(index, ViolationInvalidSpan, e, "unreachable event kind")
	}
}

// ApplyBatch feeds events in order and returns every violation produced.
func (m *Monitor) ApplyBatch(events []Event) []Violation {
	var found []Violation
	for _, e := range events {
		if v := m.Apply(e); v != nil {
			found = append(found, *v)
		}
	}
	return found
}

func (m *Monitor) applyBegin(index int, e Event) *Violation {
	field := &fieldState{
		name:      e.Name,
		path:      e.Path,
		declared:  e.Span,
		actualEnd: -1,
		depth:     len(m.stack),
		open:      true,
	}
	m.fields = append(m.fields, field)

	if len(m.stack) > 0 {
		parent := m.stack[len(m.stack)-1]
		if e.Span.Start < parent.declared.Start || e.Span.End > parent.declared.End {
			v := m.failPtr(index, ViolationNesting, e,
				fmt.Sprintf("field %s %v crosses parent %s %v",
					e.Path, e.Span, parent.path, parent.declared))
			m.stack = append(m.stack, field)
			return v
		}
	}
	m.stack = append(m.stack, field)
	return nil
}

func (m *Monitor) applyEnd(index int, e Event) *Violation {
	pos := -1
	for i := len(m.stack) - 1; i >= 0; i-- {
		if m.stack[i].path == e.Path {
			pos = i
			break
		}
	}
	if pos < 0 {
		return m.failPtr(index, ViolationUnbalancedEnd, e,
			fmt.Sprintf("end event for %s has no open field", e.Path))
	}

	for i := len(m.stack) - 1; i >= pos; i-- {
		field := m.stack[i]
		field.open = false
		field.actualEnd = e.Span.End
		if e.Span.End < field.declared.End {
			field.truncated = true
		}
	}
	m.stack = m.stack[:pos]
	return nil
}

func (m *Monitor) applyConsume(index int, e Event) *Violation {
	if hits := overlapsAny(m.discarded, e.Span); len(hits) > 0 {
		hit := hits[0]
		v := m.failPtr(index, ViolationConsumedDiscard, e,
			fmt.Sprintf("consume %v reuses discarded bytes %v", e.Span, hit))
		v.FirstBadByte = max(e.Span.Start, hit.Start)
		return v
	}
	m.consumed = addMerged(m.consumed, e.Span)
	return nil
}

func (m *Monitor) applyRecovery(index int, e Event) *Violation {
	m.recoveryCount++
	if e.Target != e.Span.End || e.Target <= e.Span.Start {
		return m.failPtr(index, ViolationInvalidSpan, e,
			"recovery target must equal span end and move forward")
	}
	discard := e.Span

	if !m.lastError {
		return m.failPtr(index, ViolationRecoverWithoutError, e,
			"recovery attempted without a pending parse error")
	}

	if hit := overlapsAny(m.consumed, discard); len(hit) > 0 {
		v := m.failPtr(index, ViolationReprocessed, e,
			fmt.Sprintf("discard %v overlaps consumed range %v", discard, hit[0]))
		v.Discard = discard
		v.Target = e.Target
		if overlap, ok := discard.Intersects(hit[0]); ok {
			v.FirstBadByte = overlap.Start
		}
		return v
	}
	if hit := overlapsAny(m.discarded, discard); len(hit) > 0 {
		v := m.failPtr(index, ViolationReprocessed, e,
			fmt.Sprintf("discard %v overlaps previously discarded range %v", discard, hit[0]))
		v.Discard = discard
		v.Target = e.Target
		if overlap, ok := discard.Intersects(hit[0]); ok {
			v.FirstBadByte = overlap.Start
		}
		return v
	}

	if !m.fieldBoundary(e.Target) {
		v := m.failPtr(index, ViolationNotFieldBoundary, e,
			fmt.Sprintf("resume offset %d is not an unconsumed field boundary", e.Target))
		v.Discard = discard
		v.Target = e.Target
		v.FirstBadByte = e.Target
		v.CandidateBoundaries = m.legalBoundariesAfter(discard.Start)
		return v
	}

	// Authenticated coverage must never be skipped, even when the resume
	// point is otherwise a valid field boundary.
	if hits := overlapsAny(m.auth, discard); len(hits) > 0 {
		v := m.failPtr(index, ViolationAuthDiscard, e,
			fmt.Sprintf("discard %v crosses authenticated coverage %v", discard, hits[0]))
		v.Discard = discard
		v.Target = e.Target
		v.AuthHits = intersectAll(hits, discard)
		if overlap, ok := discard.Intersects(hits[0]); ok {
			v.FirstBadByte = overlap.Start
		}
		return v
	}

	if containsOffset(m.consumed, e.Target) || containsOffset(m.discarded, e.Target) {
		v := m.failPtr(index, ViolationNotFieldBoundary, e,
			fmt.Sprintf("resume offset %d is already processed", e.Target))
		v.Discard = discard
		v.Target = e.Target
		v.FirstBadByte = e.Target
		v.CandidateBoundaries = m.legalBoundariesAfter(discard.Start)
		return v
	}

	m.discarded = addMerged(m.discarded, discard)
	m.lastError = false
	m.abandonOpenFields(discard)
	return nil
}

// abandonOpenFields closes fields whose bytes were skipped by recovery and
// marks them truncated, preserving them as evidence.
func (m *Monitor) abandonOpenFields(discard Interval) {
	keep := m.stack[:0]
	for _, field := range m.stack {
		// Fields fully left of the resume point were cut by the discarded
		// range. Parents that contain the resume offset stay open because
		// parsing continues inside them; their later End event determines
		// whether they were partially truncated.
		if field.declared.End <= discard.Start {
			field.open = false
			field.truncated = true
			if field.actualEnd < 0 {
				field.actualEnd = discard.Start
			}
			continue
		}
		keep = append(keep, field)
	}
	m.stack = keep
}

func (m *Monitor) legalBoundariesAfter(off int64) []int64 {
	var out []int64
	seen := map[int64]struct{}{}
	for _, field := range m.fields {
		start := field.declared.Start
		if start < off || containsOffset(m.consumed, start) || containsOffset(m.discarded, start) {
			continue
		}
		if _, ok := seen[start]; ok {
			continue
		}
		seen[start] = struct{}{}
		out = append(out, start)
		if len(out) == 4 {
			break
		}
	}
	return out
}

// newViolation builds an evidence snapshot without storing it, so callers
// can attach kind-specific detail before recording it.
func (m *Monitor) newViolation(index int, kind ViolationKind, e Event, message string) Violation {
	return Violation{
		Index:           index,
		Kind:            kind,
		Event:           e,
		RecoveryCount:   m.recoveryCount,
		Message:         message,
		OpenFields:      m.snapshotStack(),
		TruncatedFields: m.snapshotTruncated(),
	}
}

// store records a fully populated violation and returns a stable pointer to
// the stored copy.
func (m *Monitor) store(v Violation) *Violation {
	if v.Event.Kind == EventRecovery {
		v.Discard = v.Event.Span
		v.Target = v.Event.Target
	}
	m.violations = append(m.violations, v)
	return &m.violations[len(m.violations)-1]
}

func (m *Monitor) failPtr(index int, kind ViolationKind, e Event, message string) *Violation {
	return m.store(m.newViolation(index, kind, e, message))
}

func (m *Monitor) snapshotStack() []FieldInfo {
	out := make([]FieldInfo, 0, len(m.stack))
	for _, field := range m.stack {
		out = append(out, m.fieldInfo(field))
	}
	return out
}

func (m *Monitor) snapshotTruncated() []FieldInfo {
	var out []FieldInfo
	for _, field := range m.fields {
		if field.truncated || field.open {
			out = append(out, m.fieldInfo(field))
		}
	}
	return out
}

func (m *Monitor) fieldInfo(field *fieldState) FieldInfo {
	info := FieldInfo{
		Name:      field.name,
		Path:      field.path,
		Declared:  field.declared,
		Depth:     field.depth,
		Truncated: field.truncated,
		Open:      field.open,
	}
	end := field.actualEnd
	if end < 0 {
		end = m.cursor()
	}
	if end > field.declared.Start {
		info.Consumed = Interval{Start: field.declared.Start, End: min(end, field.declared.End)}
	}
	return info
}

// cursor returns the highest processed offset, used to bound open-field
// consumed ranges in evidence.
func (m *Monitor) cursor() int64 {
	hi := m.raw.Start
	for _, item := range m.consumed {
		hi = max(hi, item.End)
	}
	for _, item := range m.discarded {
		hi = max(hi, item.End)
	}
	return hi
}

// FirstViolation returns the earliest recorded violation, or nil.
func (m *Monitor) FirstViolation() *Violation {
	if len(m.violations) == 0 {
		return nil
	}
	first := m.violations[0]
	return &first
}

// Violations returns a copy of every recorded violation.
func (m *Monitor) Violations() []Violation {
	return append([]Violation(nil), m.violations...)
}

// Report snapshots all state for host-side assertions or rendering.
func (m *Monitor) Report() Report {
	fields := make([]FieldInfo, 0, len(m.fields))
	for _, field := range m.fields {
		fields = append(fields, m.fieldInfo(field))
	}
	r := Report{
		Raw:           m.raw,
		Authenticated: append([]Interval(nil), m.auth...),
		Consumed:      append([]Interval(nil), m.consumed...),
		Discarded:     append([]Interval(nil), m.discarded...),
		Fields:        fields,
		Violations:    m.Violations(),
	}
	if len(m.violations) > 0 {
		first := m.violations[0]
		r.FirstViolation = &first
	}
	return r
}
