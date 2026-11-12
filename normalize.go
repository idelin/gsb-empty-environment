package netsec

import (
	"math/big"
	"sort"
)

type sweepDelta struct {
	pos  *big.Int
	name string
	add  bool
}

// sweep merges the (possibly overlapping, aliasing) pieces into a sorted,
// pairwise disjoint write set with the full descriptor chain per segment.
func (c *Compiler) sweep(pieces []piece) []Segment {
	deltas := make([]sweepDelta, 0, len(pieces)*2)
	for _, p := range pieces {
		deltas = append(deltas,
			sweepDelta{pos: p.lo, name: p.name, add: true},
			sweepDelta{pos: p.hi, name: p.name, add: false},
		)
	}
	sort.Slice(deltas, func(i, j int) bool {
		return deltas[i].pos.Cmp(deltas[j].pos) < 0
	})

	active := make(map[string]struct{})
	segments := make([]Segment, 0)
	var prev *big.Int

	flush := func(pos *big.Int) {
		if len(active) == 0 || prev == nil || prev.Cmp(pos) == 0 {
			return
		}
		names := make([]string, 0, len(active))
		for name := range active {
			names = append(names, name)
		}
		sort.Strings(names)
		segments = append(segments, Segment{Start: prev.Uint64(), End: pos.Uint64(), Descriptors: names})
	}

	for i := 0; i < len(deltas); {
		pos := deltas[i].pos
		flush(pos)
		for i < len(deltas) && deltas[i].pos.Cmp(pos) == 0 {
			if deltas[i].add {
				active[deltas[i].name] = struct{}{}
			} else {
				delete(active, deltas[i].name)
			}
			i++
		}
		prev = new(big.Int).Set(pos)
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i].Start < segments[j].Start })
	return segments
}

// intersect returns the overlap of the canonical write set with the
// protected regions, merging adjacent pieces with the same region and
// descriptor chain.
func intersect(writeSet []Segment, protected []Region) []Violation {
	var violations []Violation
	for _, seg := range writeSet {
		for _, pr := range protected {
			lo := max(seg.Start, pr.Start)
			hi := min(seg.End, pr.End)
			if lo >= hi {
				continue
			}
			chain := append([]string(nil), seg.Descriptors...)
			violations = append(violations, Violation{Region: pr.Name, Start: lo, End: hi, Descriptors: chain})
		}
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Start != violations[j].Start {
			return violations[i].Start < violations[j].Start
		}
		return violations[i].End < violations[j].End
	})

	merged := make([]Violation, 0, len(violations))
	for _, v := range violations {
		if n := len(merged); n > 0 &&
			merged[n-1].Region == v.Region &&
			merged[n-1].End == v.Start &&
			equalChain(merged[n-1].Descriptors, v.Descriptors) {
			merged[n-1].End = v.End
			continue
		}
		merged = append(merged, v)
	}
	return merged
}

func equalChain(a, b []string) bool {
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
