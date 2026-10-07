// Package paircmp compares byte streams and remote range-readable
// entities, reporting maximal contiguous difference intervals.
package paircmp

// Interval is a half-open byte range [Start, End) in which the two
// inputs differ. Intervals are maximal: they never touch each other.
type Interval struct {
	Start, End int64
}

// TailMissing describes a trailing range that exists only in the
// longer input. Side is 0 when the left input is the shorter one,
// 1 when the right input is shorter.
type TailMissing struct {
	Side       int
	Start, End int64
}

// clipIntervals returns the intervals restricted to [0, upTo).
func clipIntervals(in []Interval, upTo int64) []Interval {
	var out []Interval
	for _, iv := range in {
		if iv.Start >= upTo {
			break
		}
		end := iv.End
		if end > upTo {
			end = upTo
		}
		out = append(out, Interval{Start: iv.Start, End: end})
	}
	return out
}
