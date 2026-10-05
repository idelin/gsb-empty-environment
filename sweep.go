package segint

import (
	"math/big"
	"sort"
)

// sweepEvent is the start or end of a segment's x-range.
type sweepEvent struct {
	x     *big.Rat
	start bool
	idx   int // index into segs
}

// IntersectSweep computes the intersection structure with a sweep line
// over x. Segments are active while the sweep position lies inside their
// (inclusive) x-range; a segment entering the active set is tested
// exactly against every currently active segment.
//
// Events at the same x are ordered with all insertions before all
// deletions. This is what makes degenerate configurations correct:
// vertical segments and zero-length segments have a zero-width x-range,
// and segments whose x-ranges only touch at a single x (endpoint
// contact) must still be simultaneously active.
func IntersectSweep(segs []Segment) Result {
	events := make([]sweepEvent, 0, 2*len(segs))
	for i, s := range segs {
		lo, hi := s.P1.X, s.P2.X
		if lo.Cmp(hi) > 0 {
			lo, hi = hi, lo
		}
		events = append(events,
			sweepEvent{x: lo, start: true, idx: i},
			sweepEvent{x: hi, start: false, idx: i},
		)
	}
	sort.Slice(events, func(i, j int) bool {
		if c := events[i].x.Cmp(events[j].x); c != 0 {
			return c < 0
		}
		if events[i].start != events[j].start {
			return events[i].start // insertions before deletions at equal x
		}
		return events[i].idx < events[j].idx
	})

	ac := newAccumulator()
	active := make(map[int]bool)
	for _, e := range events {
		if e.start {
			for other := range active {
				ac.addPair(segs[other], segs[e.idx])
			}
			active[e.idx] = true
		} else {
			delete(active, e.idx)
		}
	}
	return ac.result()
}
