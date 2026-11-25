package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Interval is a half-open time interval [Start, End).
type Interval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Valid reports whether the interval has non-zero endpoints and End is after
// Start.
func (i Interval) Valid() bool {
	return !i.Start.IsZero() && !i.End.IsZero() && i.End.After(i.Start)
}

// Contains reports whether t is inside [Start, End).
func (i Interval) Contains(t time.Time) bool {
	return !t.IsZero() && !t.Before(i.Start) && t.Before(i.End)
}

// Equal compares intervals by instant.
func (i Interval) Equal(other Interval) bool {
	return i.Start.Equal(other.Start) && i.End.Equal(other.End)
}

func (i Interval) String() string {
	return "[" + i.Start.UTC().Format(time.RFC3339Nano) + "," +
		i.End.UTC().Format(time.RFC3339Nano) + ")"
}

// TimeSpan is a normalized union of half-open intervals.  Segments are sorted,
// disjoint and non-adjacent.
type TimeSpan struct {
	Segments []Interval `json:"segments"`
}

// NewTimeSpan validates and normalizes the supplied intervals.
func NewTimeSpan(parts ...Interval) (TimeSpan, error) {
	return TimeSpan{Segments: append([]Interval(nil), parts...)}.Normalize()
}

// MustNewTimeSpan is NewTimeSpan with a panic on invalid input.
func MustNewTimeSpan(parts ...Interval) TimeSpan {
	s, err := NewTimeSpan(parts...)
	if err != nil {
		panic(err)
	}
	return s
}

// SpanOf returns the single-interval span [start, end).
func SpanOf(start, end time.Time) (TimeSpan, error) {
	return NewTimeSpan(Interval{Start: start, End: end})
}

// MustSpanOf is SpanOf with a panic on invalid input.
func MustSpanOf(start, end time.Time) TimeSpan {
	s, err := SpanOf(start, end)
	if err != nil {
		panic(err)
	}
	return s
}

// Normalize sorts, validates and merges overlapping or adjacent intervals.
// Zero-length intervals are discarded; reversed intervals are errors.
func (s TimeSpan) Normalize() (TimeSpan, error) {
	if len(s.Segments) == 0 {
		return TimeSpan{}, nil
	}
	parts := make([]Interval, 0, len(s.Segments))
	for _, iv := range s.Segments {
		if iv.Start.IsZero() || iv.End.IsZero() {
			return TimeSpan{}, fmt.Errorf("%w: zero endpoint in %s", ErrInvalidTimeSpan, iv)
		}
		if iv.End.Before(iv.Start) {
			return TimeSpan{}, fmt.Errorf("%w: end before start in %s", ErrInvalidTimeSpan, iv)
		}
		if iv.End.Equal(iv.Start) {
			continue
		}
		parts = append(parts, iv)
	}
	if len(parts) == 0 {
		return TimeSpan{}, nil
	}
	sort.SliceStable(parts, func(i, j int) bool {
		if parts[i].Start.Equal(parts[j].Start) {
			return parts[i].End.Before(parts[j].End)
		}
		return parts[i].Start.Before(parts[j].Start)
	})
	out := make([]Interval, 0, len(parts))
	cur := parts[0]
	for _, iv := range parts[1:] {
		// Adjacent intervals are merged: [a,b) U [b,c) has no gap.
		if !iv.Start.After(cur.End) {
			if iv.End.After(cur.End) {
				cur.End = iv.End
			}
			continue
		}
		out = append(out, cur)
		cur = iv
	}
	out = append(out, cur)
	return TimeSpan{Segments: out}, nil
}

// Valid reports whether the span is already normalized.
func (s TimeSpan) Valid() bool {
	norm, err := s.Normalize()
	if err != nil || len(norm.Segments) != len(s.Segments) {
		return false
	}
	for i := range norm.Segments {
		if !norm.Segments[i].Equal(s.Segments[i]) {
			return false
		}
	}
	return true
}

// Empty reports whether the span contains no instants.
func (s TimeSpan) Empty() bool {
	for _, iv := range s.Segments {
		if iv.Valid() {
			return false
		}
	}
	return true
}

// Contains reports whether t belongs to any segment.
func (s TimeSpan) Contains(t time.Time) bool {
	for _, iv := range s.Segments {
		if iv.Contains(t) {
			return true
		}
	}
	return false
}

// Intersect returns the set intersection of s and other.
func (s TimeSpan) Intersect(other TimeSpan) TimeSpan {
	a, errA := s.Normalize()
	b, errB := other.Normalize()
	if errA != nil || errB != nil {
		return TimeSpan{}
	}
	out := make([]Interval, 0, len(a.Segments)+len(b.Segments))
	i, j := 0, 0
	for i < len(a.Segments) && j < len(b.Segments) {
		start := a.Segments[i].Start
		if b.Segments[j].Start.After(start) {
			start = b.Segments[j].Start
		}
		end := a.Segments[i].End
		if b.Segments[j].End.Before(end) {
			end = b.Segments[j].End
		}
		if end.After(start) {
			out = append(out, Interval{Start: start, End: end})
		}
		if a.Segments[i].End.Before(b.Segments[j].End) {
			i++
		} else {
			j++
		}
	}
	norm, err := TimeSpan{Segments: out}.Normalize()
	if err != nil {
		return TimeSpan{}
	}
	return norm
}

// Union returns the set union of s and other.
func (s TimeSpan) Union(other TimeSpan) TimeSpan {
	all := make([]Interval, 0, len(s.Segments)+len(other.Segments))
	all = append(all, s.Segments...)
	all = append(all, other.Segments...)
	norm, err := TimeSpan{Segments: all}.Normalize()
	if err != nil {
		return TimeSpan{}
	}
	return norm
}

// IntersectAll returns the intersection of every supplied span.  With no
// arguments it returns an empty span.
func IntersectAll(spans ...TimeSpan) TimeSpan {
	if len(spans) == 0 {
		return TimeSpan{}
	}
	out, err := spans[0].Normalize()
	if err != nil {
		return TimeSpan{}
	}
	for _, s := range spans[1:] {
		out = out.Intersect(s)
		if out.Empty() {
			return TimeSpan{}
		}
	}
	return out
}

// UnionAll returns the union of every supplied span.
func UnionAll(spans ...TimeSpan) TimeSpan {
	out := TimeSpan{}
	for _, s := range spans {
		out = out.Union(s)
	}
	return out
}

// Earliest returns the earliest instant in the span.
func (s TimeSpan) Earliest() (time.Time, bool) {
	norm, err := s.Normalize()
	if err != nil || len(norm.Segments) == 0 {
		return time.Time{}, false
	}
	return norm.Segments[0].Start, true
}

// CommonInstant returns a deterministic instant contained in every span.
func CommonInstant(spans ...TimeSpan) (time.Time, bool) {
	common := IntersectAll(spans...)
	return common.Earliest()
}

// Equal compares two spans by the set of instants they contain.
func (s TimeSpan) Equal(other TimeSpan) bool {
	a, errA := s.Normalize()
	b, errB := other.Normalize()
	if errA != nil || errB != nil || len(a.Segments) != len(b.Segments) {
		return false
	}
	for i := range a.Segments {
		if !a.Segments[i].Equal(b.Segments[i]) {
			return false
		}
	}
	return true
}

// Clamp restricts the span to [from, until).  A zero endpoint means unbounded.
func (s TimeSpan) Clamp(from, until time.Time) TimeSpan {
	norm, err := s.Normalize()
	if err != nil {
		return TimeSpan{}
	}
	out := make([]Interval, 0, len(norm.Segments))
	for _, iv := range norm.Segments {
		start, end := iv.Start, iv.End
		if !from.IsZero() && from.After(start) {
			start = from
		}
		if !until.IsZero() && until.Before(end) {
			end = until
		}
		if end.After(start) {
			out = append(out, Interval{Start: start, End: end})
		}
	}
	norm, err = TimeSpan{Segments: out}.Normalize()
	if err != nil {
		return TimeSpan{}
	}
	return norm
}

func (s TimeSpan) String() string {
	norm, err := s.Normalize()
	if err != nil {
		return "<invalid>"
	}
	parts := make([]string, len(norm.Segments))
	for i, iv := range norm.Segments {
		parts[i] = iv.String()
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// MarshalJSON implements json.Marshaler.
func (s TimeSpan) MarshalJSON() ([]byte, error) {
	norm, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	if norm.Segments == nil {
		norm.Segments = []Interval{}
	}
	return json.Marshal(struct {
		Segments []Interval `json:"segments"`
	}{Segments: norm.Segments})
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *TimeSpan) UnmarshalJSON(data []byte) error {
	var wire struct {
		Segments []Interval `json:"segments"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	norm, err := TimeSpan{Segments: wire.Segments}.Normalize()
	if err != nil {
		return err
	}
	*s = norm
	return nil
}
