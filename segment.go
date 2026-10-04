package logtrunc

import (
	"errors"
	"fmt"
	"sync"
)

// SegmentID identifies a log segment.
type SegmentID string

// Segment is a contiguous, inclusive range of log sequence numbers.
type Segment struct {
	ID    SegmentID
	Start uint64
	End   uint64 // inclusive
}

// Contains reports whether seq lies inside the segment.
func (s Segment) Contains(seq uint64) bool { return seq >= s.Start && seq <= s.End }

// Overlaps reports whether the segment intersects [from, to].
func (s Segment) Overlaps(from, to uint64) bool { return s.Start <= to && from <= s.End }

// ErrSegmentNotFound is returned when deleting a segment that is already
// gone. Deletion is idempotent, so callers may treat it as success.
var ErrSegmentNotFound = errors.New("logtrunc: segment not found")

// SegmentStore is the physical storage the truncator deletes from.
// Delete must be safe to retry.
type SegmentStore interface {
	// Segments returns the live segments ordered by Start.
	Segments() []Segment
	// Delete removes a segment. It returns ErrSegmentNotFound if the
	// segment is already gone.
	Delete(id SegmentID) error
}

// MemStore is an in-memory SegmentStore, also used by tests to inject
// deletion failures.
type MemStore struct {
	mu       sync.Mutex
	segments []Segment
	// DeleteHook, when set, runs before each Delete. A non-nil result
	// aborts the deletion, simulating a retryable I/O failure.
	DeleteHook func(id SegmentID) error
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{} }

// Append adds a segment. Segments must be appended in order, contiguous,
// and non-overlapping.
func (m *MemStore) Append(seg Segment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seg.Start == 0 || seg.End < seg.Start {
		return fmt.Errorf("logtrunc: invalid segment range [%d,%d]", seg.Start, seg.End)
	}
	if n := len(m.segments); n > 0 {
		if prev := m.segments[n-1]; seg.Start != prev.End+1 {
			return fmt.Errorf("logtrunc: segment %s starts at %d, previous ends at %d", seg.ID, seg.Start, prev.End)
		}
	} else if seg.Start != 1 {
		return fmt.Errorf("logtrunc: first segment must start at 1, got %d", seg.Start)
	}
	m.segments = append(m.segments, seg)
	return nil
}

// Segments implements SegmentStore.
func (m *MemStore) Segments() []Segment {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Segment, len(m.segments))
	copy(out, m.segments)
	return out
}

// Delete implements SegmentStore.
func (m *MemStore) Delete(id SegmentID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DeleteHook != nil {
		if err := m.DeleteHook(id); err != nil {
			return err
		}
	}
	for i, seg := range m.segments {
		if seg.ID == id {
			m.segments = append(m.segments[:i], m.segments[i+1:]...)
			return nil
		}
	}
	return ErrSegmentNotFound
}

// End returns the highest sequence number in the store (0 when empty).
func (m *MemStore) End() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.segments) == 0 {
		return 0
	}
	return m.segments[len(m.segments)-1].End
}
