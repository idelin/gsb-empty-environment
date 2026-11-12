package taskpkg

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// TimeMap is a continuous piecewise-rational-linear mapping from the target
// (output) timeline to the source (media) timeline.
//
// Every calculation is performed with math/big.Rat. In particular, no
// floating-point values are used to accumulate segment lengths, slopes, or
// subtitle boundaries.
type TimeMap struct {
	segments []Segment
}

// Mapping is an alias for TimeMap.
type Mapping = TimeMap

var (
	errNoSegments = errors.New("time map has no segments")
)

// NewTimeMap validates and copies a target-to-source mapping. Segments must
// be in increasing target-time order, each target interval must be non-empty,
// and adjacent pieces must meet at the same target and source point.
//
// A freeze has a positive target interval and equal source endpoints. A
// reverse piece has SourceEnd < SourceStart.
func NewTimeMap(segments []Segment) (*TimeMap, error) {
	if len(segments) == 0 {
		return nil, errNoSegments
	}

	copied := make([]Segment, len(segments))
	for i, segment := range segments {
		if segment.TargetStart == nil || segment.TargetEnd == nil ||
			segment.SourceStart == nil || segment.SourceEnd == nil {
			return nil, fmt.Errorf("segment %d has a nil endpoint", i)
		}
		if segment.TargetStart.Cmp(segment.TargetEnd) >= 0 {
			return nil, fmt.Errorf("segment %d has non-positive target duration", i)
		}

		copied[i] = Segment{
			TargetStart: cloneRat(segment.TargetStart),
			TargetEnd:   cloneRat(segment.TargetEnd),
			SourceStart: cloneRat(segment.SourceStart),
			SourceEnd:   cloneRat(segment.SourceEnd),
		}
	}

	for i := 1; i < len(copied); i++ {
		if copied[i].TargetStart.Cmp(copied[i-1].TargetEnd) != 0 {
			return nil, fmt.Errorf(
				"segments %d and %d are not contiguous in target time: %s != %s",
				i-1, i, copied[i-1].TargetEnd.RatString(), copied[i].TargetStart.RatString(),
			)
		}
		if copied[i].SourceStart.Cmp(copied[i-1].SourceEnd) != 0 {
			return nil, fmt.Errorf(
				"segments %d and %d are discontinuous in source time: %s != %s",
				i-1, i, copied[i-1].SourceEnd.RatString(), copied[i].SourceStart.RatString(),
			)
		}
	}

	return &TimeMap{segments: copied}, nil
}

// NewMapping is an alias for NewTimeMap.
func NewMapping(segments []Segment) (*TimeMap, error) {
	return NewTimeMap(segments)
}

// MustNewTimeMap is like NewTimeMap but panics if the mapping is invalid.
func MustNewTimeMap(segments []Segment) *TimeMap {
	mapping, err := NewTimeMap(segments)
	if err != nil {
		panic(err)
	}
	return mapping
}

// TargetDomain returns the half-open target-time domain [start, end).
func (m *TimeMap) TargetDomain() (start, end *big.Rat) {
	if m == nil || len(m.segments) == 0 {
		return nil, nil
	}
	return cloneRat(m.segments[0].TargetStart), cloneRat(m.segments[len(m.segments)-1].TargetEnd)
}

// SourceDomain returns the inclusive min/max source times visited by the map.
func (m *TimeMap) SourceDomain() (min, max *big.Rat) {
	if m == nil || len(m.segments) == 0 {
		return nil, nil
	}

	min = cloneRat(m.segments[0].SourceStart)
	max = cloneRat(m.segments[0].SourceStart)
	for _, segment := range m.segments {
		if segment.SourceStart.Cmp(min) < 0 {
			min = cloneRat(segment.SourceStart)
		}
		if segment.SourceStart.Cmp(max) > 0 {
			max = cloneRat(segment.SourceStart)
		}
		if segment.SourceEnd.Cmp(min) < 0 {
			min = cloneRat(segment.SourceEnd)
		}
		if segment.SourceEnd.Cmp(max) > 0 {
			max = cloneRat(segment.SourceEnd)
		}
	}
	return min, max
}

// Segments returns a deep copy of the mapping pieces.
func (m *TimeMap) Segments() []Segment {
	if m == nil {
		return nil
	}
	out := make([]Segment, len(m.segments))
	for i, segment := range m.segments {
		out[i] = Segment{
			TargetStart: cloneRat(segment.TargetStart),
			TargetEnd:   cloneRat(segment.TargetEnd),
			SourceStart: cloneRat(segment.SourceStart),
			SourceEnd:   cloneRat(segment.SourceEnd),
		}
	}
	return out
}

// SourceAt evaluates the target-to-source mapping at target time t. The
// half-open domain's final endpoint is accepted so callers can inspect an
// interval boundary without special casing it.
func (m *TimeMap) SourceAt(target *big.Rat) (*big.Rat, error) {
	if m == nil || len(m.segments) == 0 {
		return nil, errNoSegments
	}
	if target == nil {
		return nil, errors.New("nil target time")
	}

	first := m.segments[0]
	last := m.segments[len(m.segments)-1]
	if target.Cmp(first.TargetStart) < 0 || target.Cmp(last.TargetEnd) > 0 {
		return nil, fmt.Errorf("target time %s is outside [%s, %s]",
			target.RatString(), first.TargetStart.RatString(), last.TargetEnd.RatString())
	}
	if target.Cmp(last.TargetEnd) == 0 {
		return cloneRat(last.SourceEnd), nil
	}

	index := sort.Search(len(m.segments), func(i int) bool {
		return m.segments[i].TargetEnd.Cmp(target) > 0
	})
	if index == len(m.segments) {
		return nil, fmt.Errorf("target time %s is outside the mapping", target.RatString())
	}

	segment := m.segments[index]
	if target.Cmp(segment.TargetStart) < 0 {
		return nil, fmt.Errorf("target time %s falls in an unmapped gap", target.RatString())
	}
	return sourceAt(segment, target), nil
}

// Map is an alias for SourceAt.
func (m *TimeMap) Map(target *big.Rat) (*big.Rat, error) {
	return m.SourceAt(target)
}

func sourceAt(segment Segment, target *big.Rat) *big.Rat {
	targetSpan := new(big.Rat).Sub(segment.TargetEnd, segment.TargetStart)
	offset := new(big.Rat).Sub(target, segment.TargetStart)
	position := new(big.Rat).Quo(offset, targetSpan)
	sourceSpan := new(big.Rat).Sub(segment.SourceEnd, segment.SourceStart)
	delta := new(big.Rat).Mul(position, sourceSpan)
	return new(big.Rat).Add(segment.SourceStart, delta)
}

// VisibleIntervals returns the maximal half-open target-timeline intervals
// during which subtitle is visible. A zero-length or reversed subtitle
// produces no intervals.
//
// For a reverse piece, the mathematical preimage of [Start, End) has opposite
// endpoint inclusion. This API normalizes all output to [Start, End) on the
// target axis, preserving exact rational boundaries.
func (m *TimeMap) VisibleIntervals(subtitle Subtitle) []Interval {
	if m == nil || len(m.segments) == 0 ||
		subtitle.Start == nil || subtitle.End == nil ||
		subtitle.Start.Cmp(subtitle.End) >= 0 {
		return nil
	}

	intervals := make([]Interval, 0, len(m.segments))
	for _, segment := range m.segments {
		start, end, ok := projectSegment(segment, subtitle.Start, subtitle.End)
		if !ok || start.Cmp(end) >= 0 {
			continue
		}
		intervals = append(intervals, Interval{Start: start, End: end})
	}
	return mergeIntervals(intervals)
}

func projectSegment(segment Segment, subtitleStart, subtitleEnd *big.Rat) (start, end *big.Rat, ok bool) {
	sourceSpan := new(big.Rat).Sub(segment.SourceEnd, segment.SourceStart)
	if sourceSpan.Sign() == 0 {
		if subtitleStart.Cmp(segment.SourceStart) <= 0 && segment.SourceStart.Cmp(subtitleEnd) < 0 {
			return cloneRat(segment.TargetStart), cloneRat(segment.TargetEnd), true
		}
		return nil, nil, false
	}

	startAtSourceStart := targetAtSource(segment, subtitleStart)
	endAtSourceEnd := targetAtSource(segment, subtitleEnd)

	if sourceSpan.Sign() > 0 {
		start = maxRat(segment.TargetStart, startAtSourceStart)
		end = minRat(segment.TargetEnd, endAtSourceEnd)
		return cloneRat(start), cloneRat(end), start.Cmp(end) < 0
	}

	// Source time decreases as target time advances. The visible set is
	// (endAtSourceEnd, startAtSourceStart], normalized to a half-open interval.
	start = maxRat(segment.TargetStart, endAtSourceEnd)
	end = minRat(segment.TargetEnd, startAtSourceStart)
	return cloneRat(start), cloneRat(end), start.Cmp(end) < 0
}

func targetAtSource(segment Segment, source *big.Rat) *big.Rat {
	targetSpan := new(big.Rat).Sub(segment.TargetEnd, segment.TargetStart)
	sourceSpan := new(big.Rat).Sub(segment.SourceEnd, segment.SourceStart)
	offset := new(big.Rat).Sub(source, segment.SourceStart)
	position := new(big.Rat).Quo(offset, sourceSpan)
	delta := new(big.Rat).Mul(position, targetSpan)
	return new(big.Rat).Add(segment.TargetStart, delta)
}

func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func mergeIntervals(intervals []Interval) []Interval {
	if len(intervals) == 0 {
		return nil
	}

	sort.SliceStable(intervals, func(i, j int) bool {
		if cmp := ratCmp(intervals[i].Start, intervals[j].Start); cmp != 0 {
			return cmp < 0
		}
		return ratCmp(intervals[i].End, intervals[j].End) < 0
	})

	merged := make([]Interval, 0, len(intervals))
	current := Interval{Start: cloneRat(intervals[0].Start), End: cloneRat(intervals[0].End)}
	for _, interval := range intervals[1:] {
		if interval.Start.Cmp(current.End) <= 0 {
			if interval.End.Cmp(current.End) > 0 {
				current.End = cloneRat(interval.End)
			}
			continue
		}
		merged = append(merged, current)
		current = Interval{Start: cloneRat(interval.Start), End: cloneRat(interval.End)}
	}
	return append(merged, current)
}

// Retime returns every visible occurrence of one subtitle on the target
// timeline. ID and Text are preserved exactly. Intervals are merged when they
// touch or overlap because the subtitle remains continuously visible there.
func (m *TimeMap) Retime(subtitle Subtitle) []Cue {
	intervals := m.VisibleIntervals(subtitle)
	cues := make([]Cue, 0, len(intervals))
	for _, interval := range intervals {
		cues = append(cues, Cue{
			ID:    subtitle.ID,
			Text:  subtitle.Text,
			Start: cloneRat(interval.Start),
			End:   cloneRat(interval.End),
		})
	}
	return cues
}

// RetimeAll flattens all visible subtitle occurrences. Cues are ordered by
// target start time, then by source subtitle ID. The ordering is stable for
// cues with identical start and ID. Zero-duration subtitles are omitted.
func (m *TimeMap) RetimeAll(subtitles []Subtitle) []Cue {
	var cues []Cue
	for _, subtitle := range subtitles {
		cues = append(cues, m.Retime(subtitle)...)
	}

	sort.SliceStable(cues, func(i, j int) bool {
		if cmp := cues[i].Start.Cmp(cues[j].Start); cmp != 0 {
			return cmp < 0
		}
		return cues[i].ID < cues[j].ID
	})
	return cues
}

// RetimeSubtitles keeps each source subtitle's visible intervals together.
// Subtitles with no visible interval are omitted. Groups are ordered by their
// first target start, then by source ID.
func (m *TimeMap) RetimeSubtitles(subtitles []Subtitle) []RetimedSubtitle {
	result := make([]RetimedSubtitle, 0, len(subtitles))
	for _, subtitle := range subtitles {
		intervals := m.VisibleIntervals(subtitle)
		if len(intervals) == 0 {
			continue
		}
		result = append(result, RetimedSubtitle{
			ID:        subtitle.ID,
			Text:      subtitle.Text,
			Intervals: intervals,
		})
	}

	sort.SliceStable(result, func(i, j int) bool {
		left := result[i].Intervals[0].Start
		right := result[j].Intervals[0].Start
		if cmp := left.Cmp(right); cmp != 0 {
			return cmp < 0
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// Flatten converts grouped results back to the globally sorted cue form used
// by RetimeAll, without recomputing the map.
func Flatten(subtitles []RetimedSubtitle) []Cue {
	var cues []Cue
	for _, subtitle := range subtitles {
		for _, interval := range subtitle.Intervals {
			if interval.Start == nil || interval.End == nil ||
				interval.Start.Cmp(interval.End) >= 0 {
				continue
			}
			cues = append(cues, Cue{
				ID:    subtitle.ID,
				Text:  subtitle.Text,
				Start: cloneRat(interval.Start),
				End:   cloneRat(interval.End),
			})
		}
	}
	sort.SliceStable(cues, func(i, j int) bool {
		if cmp := cues[i].Start.Cmp(cues[j].Start); cmp != 0 {
			return cmp < 0
		}
		return cues[i].ID < cues[j].ID
	})
	return cues
}
