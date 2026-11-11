package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// Range is a half-open sensitive byte interval [Start, End).
// Every evidence byte at offset off with Start <= off < End is guarded by
// Label. Ranges may overlap and carry different labels.
type Range struct {
	Start int64
	End   int64
	Label string
}

// ErrInvalidRange is returned when a sensitive range is malformed.
var ErrInvalidRange = errors.New("netsec: invalid sensitive range")

func (r Range) validate(n int64) error {
	if r.Start < 0 {
		return fmt.Errorf("%w: negative start %d", ErrInvalidRange, r.Start)
	}
	if r.End <= r.Start {
		return fmt.Errorf("%w: end %d must be greater than start %d", ErrInvalidRange, r.End, r.Start)
	}
	if r.End > n {
		return fmt.Errorf("%w: end %d exceeds evidence length %d", ErrInvalidRange, r.End, n)
	}
	if r.Label == "" {
		return fmt.Errorf("%w: empty label on [%d,%d)", ErrInvalidRange, r.Start, r.End)
	}
	return nil
}

// segment is a maximal run of consecutive offsets guarded by the same set of
// labels. Only covered offsets consume memory, so widely separated ("sparse")
// ranges stay cheap regardless of the size of the gaps between them.
type segment struct {
	start  int64
	end    int64
	labels []string // sorted, unique, non-empty
}

type sweepEvent struct {
	pos    int64
	label  string
	remove bool
}

// compileRanges deduplicates and sorts the source ranges, then sweeps the
// grouped interval boundary events to build the minimal ordered segment list.
func compileRanges(in []Range) ([]segment, []Range) {
	type rangeKey struct {
		start int64
		end   int64
		label string
	}
	seen := make(map[rangeKey]struct{}, len(in))
	uniq := make([]Range, 0, len(in))
	for _, r := range in {
		key := rangeKey{r.Start, r.End, r.Label}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, r)
	}
	sort.Slice(uniq, func(i, j int) bool {
		if uniq[i].Start != uniq[j].Start {
			return uniq[i].Start < uniq[j].Start
		}
		if uniq[i].End != uniq[j].End {
			return uniq[i].End < uniq[j].End
		}
		return uniq[i].Label < uniq[j].Label
	})

	events := make([]sweepEvent, 0, 2*len(uniq))
	for _, r := range uniq {
		events = append(events,
			sweepEvent{pos: r.Start, label: r.Label},
			sweepEvent{pos: r.End, label: r.Label, remove: true},
		)
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].pos != events[j].pos {
			return events[i].pos < events[j].pos
		}
		if events[i].remove != events[j].remove {
			return events[i].remove
		}
		return events[i].label < events[j].label
	})

	active := make(map[string]int) // label -> covering range count
	var segs []segment
	// Apply the whole event group at each distinct position first; the
	// resulting active set governs the segment starting at that position and
	// ending at the next distinct position.
	for i := 0; i < len(events); {
		pos := events[i].pos
		j := i
		for j < len(events) && events[j].pos == pos {
			if events[j].remove {
				active[events[j].label]--
				if active[events[j].label] <= 0 {
					delete(active, events[j].label)
				}
			} else {
				active[events[j].label]++
			}
			j++
		}
		i = j
		if len(active) == 0 || i >= len(events) {
			continue
		}
		next := segment{
			start:  pos,
			end:    events[i].pos,
			labels: sortedActiveLabels(active),
		}
		if last := len(segs) - 1; last >= 0 && segs[last].end == next.start && labelsEqual(segs[last].labels, next.labels) {
			segs[last].end = next.end
		} else {
			segs = append(segs, next)
		}
	}
	return segs, uniq
}

func labelsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedActiveLabels(active map[string]int) []string {
	labels := make([]string, 0, len(active))
	for label := range active {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

// maskedRanges merges every segment that contains at least one unauthorized
// label into minimal half-open proof ranges. Adjacent masked segments are
// coalesced even when their label sets differ.
func maskedRanges(segs []segment, authorized map[string]struct{}) []Range {
	var out []Range
	for _, seg := range segs {
		covered := false
		for _, label := range seg.labels {
			if _, ok := authorized[label]; !ok {
				covered = true
				break
			}
		}
		if !covered {
			continue
		}
		if last := len(out) - 1; last >= 0 && out[last].End == seg.start {
			out[last].End = seg.end
		} else {
			out = append(out, Range{Start: seg.start, End: seg.end})
		}
	}
	return out
}
