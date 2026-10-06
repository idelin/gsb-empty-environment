package settle

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Segment is a half-open rate interval [Start, End). A zero End means the
// segment is open-ended (extends to +inf). Rate is the price per unit of
// usage and must be non-nil.
type Segment struct {
	Start time.Time
	End   time.Time
	Rate  *big.Rat
}

// Contains reports whether event time t falls in [Start, End).
func (s Segment) Contains(t time.Time) bool {
	if t.Before(s.Start) {
		return false
	}
	return s.End.IsZero() || t.Before(s.End)
}

// overlap returns the length of [start, end) ∩ [s.Start, s.End).
// end must be after start.
func (s Segment) overlap(start, end time.Time) time.Duration {
	lo := s.Start
	if start.After(lo) {
		lo = start
	}
	hi := end
	if !s.End.IsZero() && s.End.Before(hi) {
		hi = s.End
	}
	if !hi.After(lo) {
		return 0
	}
	return hi.Sub(lo)
}

// Book is an ordered, non-overlapping timeline of rate segments.
type Book struct {
	segments []Segment
}

// NewBook validates and indexes the given segments. Segments must be
// sorted by Start, non-overlapping, have positive duration (unless
// open-ended), and carry a non-nil rate.
func NewBook(segments []Segment) (*Book, error) {
	if len(segments) == 0 {
		return nil, errors.New("settle: rate book needs at least one segment")
	}
	segs := make([]Segment, len(segments))
	copy(segs, segments)
	for i, s := range segs {
		if s.Rate == nil {
			return nil, fmt.Errorf("settle: segment %d has nil rate", i)
		}
		if !s.End.IsZero() && !s.End.After(s.Start) {
			return nil, fmt.Errorf("settle: segment %d has non-positive duration", i)
		}
		if i > 0 && s.Start.Before(segs[i-1].Start) {
			return nil, fmt.Errorf("settle: segment %d out of order", i)
		}
		if i > 0 {
			prev := segs[i-1]
			if prev.End.IsZero() || s.Start.Before(prev.End) {
				return nil, fmt.Errorf("settle: segment %d overlaps previous segment", i)
			}
		}
	}
	return &Book{segments: segs}, nil
}

// Segments returns a copy of the current rate timeline.
func (b *Book) Segments() []Segment {
	out := make([]Segment, len(b.segments))
	copy(out, b.segments)
	return out
}

// SegmentAt returns the segment containing event time t.
func (b *Book) SegmentAt(t time.Time) (Segment, bool) {
	for _, s := range b.segments {
		if s.Contains(t) {
			return s, true
		}
	}
	return Segment{}, false
}

// covered reports whether [start, end) is fully covered by segments.
func (b *Book) covered(start, end time.Time) bool {
	cursor := start
	for _, s := range b.segments {
		if s.End.IsZero() {
			return !s.Start.After(cursor)
		}
		if s.End.After(cursor) && !s.Start.After(cursor) {
			cursor = s.End
			if !cursor.Before(end) {
				return true
			}
		}
	}
	return false
}
