package segsweep

import (
	"math/big"
	"sort"
)

// BruteForce is an O(n^2) all-pairs reference implementation. It exists
// to cross-check Sweep, especially for vertical segments, zero-length
// segments, and multiple collinear coverage.
func BruteForce(input []IntSegment) Result {
	segs := normalizeSegments(input)
	locs := map[string]Point{}
	var ovls []pairOverlap
	for i := 0; i < len(segs); i++ {
		for j := i + 1; j < len(segs); j++ {
			pts, ov := pairIntersect(segs[i], segs[j])
			for _, p := range pts {
				locs[pointKey(p)] = p
			}
			if ov != nil {
				ovls = append(ovls, *ov)
			}
		}
	}
	// Each point location gets the full set of segments containing it.
	points := map[string]*pointAcc{}
	for k, p := range locs {
		acc := &pointAcc{p: p, ids: map[int]bool{}}
		for _, s := range segs {
			if s.contains(p) {
				acc.ids[s.id] = true
			}
		}
		if len(acc.ids) >= 2 {
			points[k] = acc
		}
	}
	return assemble(points, aggregatePairwiseOverlaps(ovls))
}

type pairOverlap struct {
	rep      *seg
	lo, hi   *big.Rat // 1D parameters on rep's supporting line, lo < hi
	id1, id2 int
}

// pairIntersect computes the intersection of two segments: either a
// single point, a positive-length collinear overlap, or nothing.
func pairIntersect(s1, s2 *seg) ([]Point, *pairOverlap) {
	if s1.kind == kindPoint && s2.kind == kindPoint {
		if s1.a.Cmp(s2.a) == 0 {
			return []Point{s1.a}, nil
		}
		return nil, nil
	}
	if s1.kind == kindPoint {
		if s2.contains(s1.a) {
			return []Point{s1.a}, nil
		}
		return nil, nil
	}
	if s2.kind == kindPoint {
		if s1.contains(s2.a) {
			return []Point{s2.a}, nil
		}
		return nil, nil
	}
	if s1.line == s2.line {
		lo1, hi1 := s1.a.X, s1.b.X
		lo2, hi2 := s2.a.X, s2.b.X
		if s1.kind == kindVertical {
			lo1, hi1 = s1.a.Y, s1.b.Y
			lo2, hi2 = s2.a.Y, s2.b.Y
		}
		mx := lo1
		if lo2.Cmp(mx) > 0 {
			mx = lo2
		}
		mn := hi1
		if hi2.Cmp(mn) < 0 {
			mn = hi2
		}
		switch c := mx.Cmp(mn); {
		case c > 0:
			return nil, nil
		case c == 0:
			return []Point{linePointAt(s1, mx)}, nil
		default:
			return nil, &pairOverlap{rep: s1, lo: mx, hi: mn, id1: s1.id, id2: s2.id}
		}
	}
	p, ok := lineIntersection(s1, s2)
	if !ok {
		return nil, nil
	}
	if s1.contains(p) && s2.contains(p) {
		return []Point{p}, nil
	}
	return nil, nil
}

// aggregatePairwiseOverlaps merges pairwise collinear overlap intervals
// into maximal regions with a constant participant set.
func aggregatePairwiseOverlaps(ovls []pairOverlap) []Overlap {
	byLine := map[string][]pairOverlap{}
	for _, o := range ovls {
		byLine[o.rep.line] = append(byLine[o.rep.line], o)
	}
	var out []Overlap
	for _, group := range byLine {
		rep := group[0].rep
		tset := map[string]*big.Rat{}
		for _, o := range group {
			if tset[o.lo.String()] == nil {
				tset[o.lo.String()] = o.lo
			}
			if tset[o.hi.String()] == nil {
				tset[o.hi.String()] = o.hi
			}
		}
		ts := make([]*big.Rat, 0, len(tset))
		for _, t := range tset {
			ts = append(ts, t)
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i].Cmp(ts[j]) < 0 })
		var pending *Overlap
		flush := func() {
			if pending != nil {
				out = append(out, *pending)
				pending = nil
			}
		}
		for i := 0; i+1 < len(ts); i++ {
			set := map[int]bool{}
			for _, o := range group {
				if o.lo.Cmp(ts[i]) <= 0 && o.hi.Cmp(ts[i+1]) >= 0 {
					set[o.id1] = true
					set[o.id2] = true
				}
			}
			if len(set) < 2 {
				flush()
				continue
			}
			ids := make([]int, 0, len(set))
			for id := range set {
				ids = append(ids, id)
			}
			sort.Ints(ids)
			if pending != nil && equalInts(pending.IDs, ids) {
				pending.B = linePointAt(rep, ts[i+1])
				continue
			}
			flush()
			pending = &Overlap{
				A:   linePointAt(rep, ts[i]),
				B:   linePointAt(rep, ts[i+1]),
				IDs: ids,
			}
		}
		flush()
	}
	return out
}
