package cachemodel

// interval is a half-open byte range [start, end) tagged with the page
// version at which those bytes were last dirtied.
type interval struct {
	start, end int
	ver        uint64
}

// dirtySet is a sorted, non-overlapping set of dirty byte intervals.
// Each interval carries the version of the write that last touched it,
// so a stale writeback completion can tell "bytes I wrote back" apart
// from "bytes re-dirtied after my snapshot".
type dirtySet struct {
	ivs []interval
}

func (d *dirtySet) empty() bool { return len(d.ivs) == 0 }

// snapshot returns a copy of the current intervals.
func (d *dirtySet) snapshot() []interval {
	out := make([]interval, len(d.ivs))
	copy(out, d.ivs)
	return out
}

// add marks [start, end) dirty at version ver, overwriting any prior
// dirty information in that range.
func (d *dirtySet) add(start, end int, ver uint64) {
	if start >= end {
		return
	}
	out := make([]interval, 0, len(d.ivs)+2)
	inserted := false
	for _, iv := range d.ivs {
		switch {
		case iv.end <= start:
			out = append(out, iv)
		case iv.start >= end:
			if !inserted {
				out = append(out, interval{start, end, ver})
				inserted = true
			}
			out = append(out, iv)
		default:
			if iv.start < start {
				out = append(out, interval{iv.start, start, iv.ver})
			}
			if !inserted {
				out = append(out, interval{start, end, ver})
				inserted = true
			}
			if iv.end > end {
				out = append(out, interval{end, iv.end, iv.ver})
			}
		}
	}
	if !inserted {
		out = append(out, interval{start, end, ver})
	}
	d.ivs = mergeAdjacent(out)
}

// clearAcked removes the intersection with each acked range, but only
// for bytes whose dirty version is <= ver (i.e. bytes covered by the
// writeback snapshot). Bytes re-dirtied after the snapshot have a higher
// version and survive.
func (d *dirtySet) clearAcked(acked []interval, ver uint64) {
	for _, a := range acked {
		out := make([]interval, 0, len(d.ivs)+1)
		for _, iv := range d.ivs {
			if iv.end <= a.start || iv.start >= a.end || iv.ver > ver {
				out = append(out, iv)
				continue
			}
			lo := iv.start
			if a.start > lo {
				lo = a.start
			}
			hi := iv.end
			if a.end < hi {
				hi = a.end
			}
			if iv.start < lo {
				out = append(out, interval{iv.start, lo, iv.ver})
			}
			if hi < iv.end {
				out = append(out, interval{hi, iv.end, iv.ver})
			}
		}
		d.ivs = out
	}
}

func mergeAdjacent(ivs []interval) []interval {
	out := ivs[:0]
	for _, iv := range ivs {
		if n := len(out); n > 0 && out[n-1].end == iv.start && out[n-1].ver == iv.ver {
			out[n-1].end = iv.end
			continue
		}
		out = append(out, iv)
	}
	return out
}
