package netsec

import (
	"fmt"
	"sync"
)

// Reason classifies why a transmitted byte is residual.
type Reason string

const (
	// NeverWritten marks a byte that no Write event has initialized since the
	// monitored object was created.
	NeverWritten Reason = "never-written"

	// StaleMember marks a byte last written through a union member that is no
	// longer the active member for the field being sent. The byte value is left over
	// from the previous active member because a switch does not clear storage.
	StaleMember Reason = "stale-union-member"
)

// EventKind identifies the kind of instrumentation event recorded by a
// Monitor.
type EventKind int

const (
	EventSwitch EventKind = iota
	EventWrite
	EventSend
)

func (k EventKind) String() string {
	switch k {
	case EventSwitch:
		return "switch"
	case EventWrite:
		return "write"
	case EventSend:
		return "send"
	default:
		return fmt.Sprintf("event(%d)", int(k))
	}
}

// Event is one instrumentation input consumed by the Monitor. Offset and Size
// are absolute object offsets and byte counts.
type Event struct {
	Seq    int64
	Kind   EventKind
	Path   string
	Offset int
	Size   int
}

// provenance records the last writer of a single byte. A nil entry means the
// byte has never been initialized.
//
// membership snapshots the active member of every union ancestor of the writer
// at write time. A byte is safe to send through a target only while the
// target's union selections still match this snapshot for the unions they
// share. That captures partial overwrites correctly: a byte rewritten through
// the currently active member carries a fresh snapshot even if the earlier
// writer was another member.
type provenance struct {
	writerPath string
	membership map[*node]*node
}

// Monitor models one instrumented object. It is safe for concurrent use.
type Monitor struct {
	mu     sync.Mutex
	root   *node
	size   int
	prov   []*provenance
	events []Event
	seq    int64
}

// NewMonitor validates the given explicit layout and returns an empty monitor.
// Every byte starts uninitialized; no zero-initialization is performed, because
// the monitor tracks instrumented writes rather than fabricating them.
func NewMonitor(layout Field) (*Monitor, error) {
	if layout.Offset != 0 {
		return nil, fmt.Errorf("netsec: root field %q must start at offset 0, got %d",
			layout.Name, layout.Offset)
	}
	byPath := make(map[string]*node)
	root, err := buildNode(layout, "", byPath)
	if err != nil {
		return nil, err
	}
	root.byPath = byPath
	return &Monitor{
		root: root,
		size: root.field.Size,
		prov: make([]*provenance, root.field.Size),
	}, nil
}

// Size reports the total object size in bytes.
func (m *Monitor) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.size
}

// Events returns a copy of the instrumentation events consumed so far, in the
// order they were received.
func (m *Monitor) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Switch selects memberPath as the active member of the union at unionPath.
//
// A switch changes only which member is active. It never initializes, clears or
// rewrites any byte, including bytes outside the previous member's span.
func (m *Monitor) Switch(unionPath, memberPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	unionNode, err := m.lookupLocked(unionPath)
	if err != nil {
		return err
	}
	if unionNode.field.Kind != Union {
		return fmt.Errorf("netsec: switch target %q is %s, not union", unionPath, unionNode.field.Kind)
	}
	member, err := m.lookupLocked(memberPath)
	if err != nil {
		return err
	}
	if member.parent != unionNode {
		return fmt.Errorf("netsec: %q is not a direct member of union %q", memberPath, unionPath)
	}

	unionNode.active = member
	m.recordLocked(EventSwitch, memberPath, member.field.Offset, member.field.Size)
	return nil
}

// Write records a byte-wise write of length bytes to fieldPath starting at
// offset within that field. Each written byte is attributed to fieldPath and
// stamped with the current union membership, overwriting any earlier
// provenance.
func (m *Monitor) Write(fieldPath string, offset, length int) error {
	if length <= 0 {
		return fmt.Errorf("netsec: write length must be positive, got %d", length)
	}
	if offset < 0 {
		return fmt.Errorf("netsec: write offset must not be negative, got %d", offset)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target, err := m.lookupLocked(fieldPath)
	if err != nil {
		return err
	}
	start := target.field.Offset + offset
	end := start + length
	if end > target.field.Offset+target.field.Size || end > m.size {
		return fmt.Errorf("netsec: write to %q (offset=%d length=%d) overflows field size %d",
			fieldPath, offset, length, target.field.Size)
	}
	if err := checkActiveLocked(target); err != nil {
		return err
	}

	m.recordLocked(EventWrite, fieldPath, start, length)
	for abs := start; abs < end; abs++ {
		m.prov[abs] = &provenance{
			writerPath: target.path,
			membership: activeMembership(target),
		}
	}
	return nil
}

// Residual describes a contiguous run of bytes that a send would transmit
// without a valid, current provenance. Offset is an absolute object offset.
type Residual struct {
	Offset int
	Length int
	Reason Reason

	// Source is the dot-separated path of the field that last wrote the bytes.
	// It is empty for NeverWritten residuals.
	Source string
}

// String renders the run for diagnostics.
func (r Residual) String() string {
	if r.Source == "" {
		return fmt.Sprintf("bytes [%d,%d) %s", r.Offset, r.Offset+r.Length, r.Reason)
	}
	return fmt.Sprintf("bytes [%d,%d) %s from %q", r.Offset, r.Offset+r.Length, r.Reason, r.Source)
}

// Report is the result of a Send inspection.
type Report struct {
	// Field is the path that was sent.
	Field string
	// Offset and Size delimit the sent span as absolute object offsets.
	Offset int
	Size   int
	// Clean is true when every sent byte is initialized through the currently
	// active member chain.
	Clean bool
	// Residuals lists contiguous residual runs, ordered by offset.
	Residuals []Residual
}

// ResidualBytes counts how many transmitted bytes are residual.
func (r Report) ResidualBytes() int {
	total := 0
	for _, run := range r.Residuals {
		total += run.Length
	}
	return total
}

// Send validates a send of length bytes from fieldPath starting at offset.
//
// The field must be reachable through the active member of every enclosing
// union. Only bytes initialized through that active member chain are clean:
// uninitialized bytes and bytes left over from an inactive union member are
// reported as residual with their source.
func (m *Monitor) Send(fieldPath string, offset, length int) (Report, error) {
	if length <= 0 {
		return Report{}, fmt.Errorf("netsec: send length must be positive, got %d", length)
	}
	if offset < 0 {
		return Report{}, fmt.Errorf("netsec: send offset must not be negative, got %d", offset)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target, err := m.lookupLocked(fieldPath)
	if err != nil {
		return Report{}, err
	}
	start := target.field.Offset + offset
	end := start + length
	if end > target.field.Offset+target.field.Size || end > m.size {
		return Report{}, fmt.Errorf("netsec: send of %q (offset=%d length=%d) overflows field size %d",
			fieldPath, offset, length, target.field.Size)
	}
	if err := checkActiveLocked(target); err != nil {
		return Report{}, err
	}

	report := Report{Field: fieldPath, Offset: start, Size: length, Clean: true}
	for abs := start; abs < end; {
		reason, source := m.classifyLocked(abs, target)
		if reason == "" {
			abs++
			continue
		}

		runStart := abs
		for abs < end {
			nextReason, nextSource := m.classifyLocked(abs, target)
			if nextReason != reason || nextSource != source {
				break
			}
			abs++
		}
		report.Clean = false
		report.Residuals = append(report.Residuals, Residual{
			Offset: runStart,
			Length: abs - runStart,
			Reason: reason,
			Source: source,
		})
	}

	m.recordLocked(EventSend, fieldPath, start, length)
	return report, nil
}

// InitializedSpan counts how many bytes in the given field range have ever
// been written. It ignores union activity, so it answers only whether the
// storage has any provenance at all.
func (m *Monitor) InitializedSpan(fieldPath string, offset, length int) (int, error) {
	if length <= 0 {
		return 0, fmt.Errorf("netsec: span length must be positive, got %d", length)
	}
	if offset < 0 {
		return 0, fmt.Errorf("netsec: span offset must not be negative, got %d", offset)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target, err := m.lookupLocked(fieldPath)
	if err != nil {
		return 0, err
	}
	start := target.field.Offset + offset
	end := start + length
	if end > target.field.Offset+target.field.Size || end > m.size {
		return 0, fmt.Errorf("netsec: span of %q (offset=%d length=%d) overflows field size %d",
			fieldPath, offset, length, target.field.Size)
	}

	count := 0
	for abs := start; abs < end; abs++ {
		if m.prov[abs] != nil {
			count++
		}
	}
	return count, nil
}

func (m *Monitor) lookupLocked(path string) (*node, error) {
	if path == "" || path == m.root.path {
		return m.root, nil
	}
	target, ok := m.root.byPath[path]
	if ok {
		return target, nil
	}
	// Accept root-relative paths ("body.u32v") as well as fully qualified
	// paths ("msg.body.u32v").
	if target, ok = m.root.byPath[m.root.path+"."+path]; ok {
		return target, nil
	}
	return nil, fmt.Errorf("netsec: unknown field path %q", path)
}

func (m *Monitor) recordLocked(kind EventKind, path string, offset, size int) {
	m.seq++
	m.events = append(m.events, Event{
		Seq:    m.seq,
		Kind:   kind,
		Path:   path,
		Offset: offset,
		Size:   size,
	})
}

// classifyLocked returns the residual reason and source path for one byte. An
// empty reason means the byte is safe to send through target.
func (m *Monitor) classifyLocked(abs int, target *node) (Reason, string) {
	p := m.prov[abs]
	if p == nil {
		return NeverWritten, ""
	}
	if isStale(p.membership, target) {
		return StaleMember, p.writerPath
	}
	return "", ""
}

// checkActiveLocked ensures target is reachable through the active member of
// every union on its ancestor chain.
func checkActiveLocked(target *node) error {
	child := target
	for parent := target.parent; parent != nil; parent = parent.parent {
		if parent.field.Kind == Union {
			switch {
			case parent.active == nil:
				return fmt.Errorf("netsec: cannot send %q: union %q has no active member",
					target.path, parent.path)
			case parent.active != child:
				return fmt.Errorf("netsec: cannot send %q: inactive member of union %q (active %q)",
					target.path, parent.path, parent.active.path)
			}
		}
		child = parent
	}
	return nil
}

// activeMembership maps every union ancestor of n to the member that is active
// on the path from that union down to n.
func activeMembership(n *node) map[*node]*node {
	membership := make(map[*node]*node)
	child := n
	for parent := n.parent; parent != nil; parent = parent.parent {
		if parent.field.Kind == Union {
			membership[parent] = child
		}
		child = parent
	}
	return membership
}

// isStale reports whether a byte whose write-time membership is snapshotted
// cannot legally supply target: they must select the same member for every
// union ancestor that appears in both paths.
func isStale(snapshot map[*node]*node, target *node) bool {
	child := target
	for parent := target.parent; parent != nil; parent = parent.parent {
		if parent.field.Kind == Union {
			if chosen, ok := snapshot[parent]; ok && chosen != child {
				return true
			}
		}
		child = parent
	}
	return false
}
