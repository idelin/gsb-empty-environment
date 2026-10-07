package perflib

import "sort"

// FuncStat aggregates per-name timing across all threads.
type FuncStat struct {
	Name  string
	Calls int
	// Inclusive sums per-call inclusive spans. Calls of one name never
	// overlap on a single thread (stack discipline), but async calls on
	// different threads may overlap, so cross-thread wall time must use
	// WallTime/FuncWallTime instead of this sum.
	Inclusive int64
	// Exclusive sums per-call exclusive spans.
	Exclusive int64
}

// Stats aggregates every frame by function name.
func (t *Trace) Stats() map[string]*FuncStat {
	stats := map[string]*FuncStat{}
	Walk(t.Roots, func(n *CallNode) {
		s := stats[n.Name]
		if s == nil {
			s = &FuncStat{Name: n.Name}
			stats[n.Name] = s
		}
		s.Calls++
		s.Inclusive += n.Inclusive()
		s.Exclusive += n.Exclusive()
	})
	return stats
}

// Segment is one maximal time slice with constant occupancy.
type Segment struct {
	Start int64
	End   int64
	Depth int // number of intervals covering [Start, End)
}

// Occupancy sweeps intervals into constant-depth time slices. This is
// the time-slice occupancy model: at any instant, Depth counts how
// many frames (across all contributing threads) are active.
func Occupancy(ivs []Interval) []Segment {
	type edge struct {
		t     int64
		delta int
	}
	edges := make([]edge, 0, 2*len(ivs))
	for _, iv := range ivs {
		if iv.End <= iv.Start {
			continue
		}
		edges = append(edges, edge{iv.Start, 1}, edge{iv.End, -1})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].t < edges[j].t })

	var segs []Segment
	depth, prev := 0, int64(0)
	for i := 0; i < len(edges); {
		t := edges[i].t
		if depth > 0 && t > prev {
			segs = append(segs, Segment{Start: prev, End: t, Depth: depth})
		}
		for ; i < len(edges) && edges[i].t == t; i++ {
			depth += edges[i].delta
		}
		prev = t
	}
	return segs
}

// UnionDuration is the total length of time slices covered by at least
// one interval. Overlapping (e.g. async) spans are counted once.
func UnionDuration(ivs []Interval) int64 {
	var total int64
	for _, s := range Occupancy(ivs) {
		total += s.End - s.Start
	}
	return total
}

func (t *Trace) intervals(filter func(*CallNode) bool) []Interval {
	var ivs []Interval
	Walk(t.Roots, func(n *CallNode) {
		if filter == nil || filter(n) {
			ivs = append(ivs, n.Span())
		}
	})
	return ivs
}

// WallTime is the union of all call spans: how long the profiled
// program had any work in flight, with async overlaps counted once.
func (t *Trace) WallTime() int64 {
	return UnionDuration(t.intervals(nil))
}

// FuncWallTime maps each function name to the union of its call spans
// across all threads, so overlapping async invocations are not summed.
func (t *Trace) FuncWallTime() map[string]int64 {
	byName := map[string][]Interval{}
	Walk(t.Roots, func(n *CallNode) {
		byName[n.Name] = append(byName[n.Name], n.Span())
	})
	out := make(map[string]int64, len(byName))
	for name, ivs := range byName {
		out[name] = UnionDuration(ivs)
	}
	return out
}
