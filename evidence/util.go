package evidence

// intersect returns the overlap of two half-open ranges; the result has zero
// length when they are disjoint.
func intersect(a, b Range) Range {
	start := a.Start
	if b.Start > start {
		start = b.Start
	}
	end := a.End
	if b.End < end {
		end = b.End
	}
	if start >= end {
		return Range{Start: start, End: start}
	}
	return Range{Start: start, End: end}
}

// fragment is a contiguous slice of a write not already retained.
type fragment struct {
	start int64
	end   int64
	data  []byte
}

// splitNewFragments removes every already-covered sub-range from the write at
// off and returns the remaining pieces, preserving byte content. Coverage must
// hold disjoint, sorted ranges.
func splitNewFragments(off int64, p []byte, coverage []Range) []fragment {
	var frags []fragment
	cursor := off
	end := off + int64(len(p))

	flush := func(fragEnd int64) {
		if fragEnd <= cursor {
			return
		}
		frags = append(frags, fragment{
			start: cursor,
			end:   fragEnd,
			data:  p[cursor-off : fragEnd-off],
		})
	}

	for _, covered := range coverage {
		if covered.End <= cursor {
			continue
		}
		if covered.Start >= end {
			break
		}
		if covered.Start > cursor {
			flush(covered.Start)
		}
		if covered.End > cursor {
			cursor = covered.End
		}
	}
	flush(end)
	return frags
}
