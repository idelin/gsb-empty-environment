package netsec

import (
	"bytes"
	"sort"
)

func hashEqual(a, b ContentHash) bool {
	return bytes.Equal(a, b)
}

// safeNext returns n+1, clamping at the Seq maximum to avoid overflow.
func safeNext(n Seq) Seq {
	if n == ^Seq(0) {
		return n
	}
	return n + 1
}

// sortedKeys returns the sorted keys of any map keyed by Seq.
func sortedKeys[V any](m map[Seq]V) []Seq {
	out := make([]Seq, 0, len(m))
	for seq := range m {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// pointsToIntervals coalesces sorted, de-duplicated sequence points into
// inclusive intervals.
func pointsToIntervals(points []Seq) []Interval {
	if len(points) == 0 {
		return nil
	}

	intervals := make([]Interval, 0, len(points))
	start := points[0]
	prev := points[0]
	for _, seq := range points[1:] {
		if seq == prev {
			continue
		}
		if seq != safeNext(prev) {
			intervals = append(intervals, Interval{From: start, To: prev})
			start = seq
		}
		prev = seq
	}
	intervals = append(intervals, Interval{From: start, To: prev})
	return intervals
}

// invertPoints returns the complement of the sorted, de-duplicated points
// within the inclusive universe [from, to].
func invertPoints(from, to Seq, points []Seq) []Interval {
	if from > to {
		return nil
	}

	var missing []Seq
	cursor := from
	for _, seq := range points {
		if seq < from || seq > to {
			continue
		}
		for cursor < seq {
			missing = append(missing, cursor)
			cursor = safeNext(cursor)
		}
		if cursor == seq {
			cursor = safeNext(cursor)
		}
	}
	for cursor <= to && cursor != 0 {
		missing = append(missing, cursor)
		if cursor == ^Seq(0) {
			break
		}
		cursor++
	}
	return pointsToIntervals(missing)
}

// invertAbove returns intervals of present sequences strictly above the
// watermark (i.e. records received ahead of any issuance).
func invertAbove(watermark Seq, present map[Seq]ContentHash) []Interval {
	fringe := make([]Seq, 0)
	for seq := range present {
		if seq > watermark {
			fringe = append(fringe, seq)
		}
	}
	sort.Slice(fringe, func(i, j int) bool { return fringe[i] < fringe[j] })
	return pointsToIntervals(fringe)
}
