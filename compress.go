package timemap

import (
	"errors"
	"fmt"
	"sort"
)

// Segment kinds produced by the compressor.
const (
	SegProtected      = "protected"
	SegIdleCompressed = "idle-compressed"
	SegIdleKept       = "idle-kept"
)

// Segment is one piece of the compressed timeline, annotated with the reason
// the compressor chose its slope.
type Segment struct {
	Kind        string
	Reason      string
	ProtectedID string
	Start       Rat // original start
	End         Rat // original end
	MappedStart Rat // compressed start
	MappedEnd   Rat // compressed end
}

// OriginalInterval returns the segment on the original timeline.
func (s Segment) OriginalInterval() Interval {
	return Interval{ID: s.ProtectedID, Kind: s.Kind, Start: s.Start, End: s.End}
}

// MappedInterval returns the segment on the compressed timeline.
func (s Segment) MappedInterval() Interval {
	return Interval{ID: s.ProtectedID, Kind: s.Kind, Start: s.MappedStart, End: s.MappedEnd}
}

// Slope returns the compression slope of the segment.
func (s Segment) Slope() Rat {
	return s.MappedEnd.Sub(s.MappedStart).Div(s.End.Sub(s.Start))
}

// Spec configures the compressor.
type Spec struct {
	// MinIdleGap: idle stretches at least this long are eligible for compression.
	MinIdleGap Rat
	// CompressedDuration: the duration an eligible idle stretch is squeezed to.
	CompressedDuration Rat
	// Events: event times that must keep their order and simultaneity.
	Events []Rat
	// Protected: intervals that must stay 1:1 (mouse drags, continuous audio).
	Protected []Interval
}

// Result pairs the shared mapping with the per-segment rationale.
type Result struct {
	Mapping  *Mapping
	Segments []Segment
}

// Build constructs the shared monotonic piecewise-linear mapping.
//
// The timeline is cut at every protected boundary and every event time, so no
// segment interior contains an event or crosses a protected edge. Protected
// pieces keep slope 1; idle pieces at least MinIdleGap long are compressed to
// CompressedDuration; shorter idle pieces keep slope 1. Every piece has
// positive slope, so the composed map is strictly increasing and invertible.
func Build(spec Spec) (*Result, error) {
	if !spec.MinIdleGap.valid() || spec.MinIdleGap.Sign() <= 0 {
		return nil, errors.New("MinIdleGap must be positive")
	}
	if !spec.CompressedDuration.valid() || spec.CompressedDuration.Sign() <= 0 {
		return nil, errors.New("CompressedDuration must be positive")
	}
	if spec.CompressedDuration.Cmp(spec.MinIdleGap) >= 0 {
		return nil, fmt.Errorf("CompressedDuration (%s) must be smaller than MinIdleGap (%s)",
			spec.CompressedDuration, spec.MinIdleGap)
	}

	protected, err := normalizeProtected(spec.Protected)
	if err != nil {
		return nil, err
	}

	cuts := map[string]Rat{}
	for _, iv := range protected {
		cuts[iv.Start.String()] = iv.Start
		cuts[iv.End.String()] = iv.End
	}
	for _, t := range spec.Events {
		if !t.valid() {
			return nil, errors.New("event with nil time")
		}
		cuts[t.String()] = t
	}
	if len(cuts) < 2 {
		return nil, errors.New("need at least two distinct cut points (events or protected boundaries)")
	}
	xs := make([]Rat, 0, len(cuts))
	for _, t := range cuts {
		xs = append(xs, t)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i].Cmp(xs[j]) < 0 })

	var (
		pts      []Point
		segments []Segment
	)
	y := xs[0] // anchor: the earliest cut maps to itself
	pts = append(pts, Point{X: xs[0], Y: y})
	for i := 0; i+1 < len(xs); i++ {
		start, end := xs[i], xs[i+1]
		dur := end.Sub(start)
		seg := Segment{Start: start, End: end, MappedStart: y}
		if iv, ok := coveringProtected(protected, start, end); ok {
			seg.Kind = SegProtected
			seg.ProtectedID = iv.ID
			seg.Reason = fmt.Sprintf("protected %s interval %q stays 1:1", iv.Kind, iv.ID)
			seg.MappedEnd = y.Add(dur)
		} else if dur.Cmp(spec.MinIdleGap) >= 0 {
			seg.Kind = SegIdleCompressed
			seg.Reason = fmt.Sprintf("idle %s >= min gap %s: compressed to %s (slope %s)",
				dur, spec.MinIdleGap, spec.CompressedDuration,
				spec.CompressedDuration.Div(dur))
			seg.MappedEnd = y.Add(spec.CompressedDuration)
		} else {
			seg.Kind = SegIdleKept
			seg.Reason = fmt.Sprintf("idle %s < min gap %s: kept 1:1", dur, spec.MinIdleGap)
			seg.MappedEnd = y.Add(dur)
		}
		y = seg.MappedEnd
		pts = append(pts, Point{X: end, Y: y})
		segments = append(segments, seg)
	}

	m, err := NewMapping(pts)
	if err != nil {
		return nil, err
	}
	return &Result{Mapping: m, Segments: segments}, nil
}

// normalizeProtected validates protected intervals and merges overlapping or
// touching ones so every timeline point has at most one protector.
func normalizeProtected(in []Interval) ([]Interval, error) {
	var ivs []Interval
	for _, iv := range in {
		if !iv.Start.valid() || !iv.End.valid() {
			return nil, fmt.Errorf("protected interval %q has nil bound", iv.ID)
		}
		if iv.Start.Cmp(iv.End) >= 0 {
			return nil, fmt.Errorf("protected interval %q has empty or negative span [%s, %s)", iv.ID, iv.Start, iv.End)
		}
		ivs = append(ivs, iv)
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].Start.Cmp(ivs[j].Start) < 0 })
	var merged []Interval
	for _, iv := range ivs {
		if n := len(merged); n > 0 && merged[n-1].End.Cmp(iv.Start) >= 0 {
			if merged[n-1].End.Cmp(iv.End) < 0 {
				merged[n-1].End = iv.End
			}
			merged[n-1].ID = merged[n-1].ID + "+" + iv.ID
			merged[n-1].Kind = merged[n-1].Kind + "+" + iv.Kind
			continue
		}
		merged = append(merged, iv)
	}
	return merged, nil
}

// coveringProtected reports whether [start, end) lies inside one protected interval.
func coveringProtected(protected []Interval, start, end Rat) (Interval, bool) {
	for _, iv := range protected {
		if iv.Start.Cmp(start) <= 0 && end.Cmp(iv.End) <= 0 {
			return iv, true
		}
	}
	return Interval{}, false
}
