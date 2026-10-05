package segsweep

import (
	"math/big"
	"sort"
)

// computeOverlaps groups non-point segments by supporting line and, for
// each line, reports maximal positive-length portions covered by two or
// more segments (with the constant participant set). Positions where
// collinear segments only touch at a single point are reported as
// intersection points via record.
func computeOverlaps(segs []*seg, record func(Point, []int)) []Overlap {
	byLine := map[string][]*seg{}
	for _, s := range segs {
		byLine[s.line] = append(byLine[s.line], s)
	}
	var overlaps []Overlap
	for _, group := range byLine {
		if len(group) < 2 {
			continue
		}
		rep := group[0]
		vertical := rep.kind == kindVertical
		type iv struct {
			lo, hi *big.Rat
			id     int
		}
		ivs := make([]iv, 0, len(group))
		tset := map[string]*big.Rat{}
		for _, s := range group {
			lo, hi := s.a.X, s.b.X
			if vertical {
				lo, hi = s.a.Y, s.b.Y
			}
			ivs = append(ivs, iv{lo, hi, s.id})
			if tset[lo.String()] == nil {
				tset[lo.String()] = lo
			}
			if tset[hi.String()] == nil {
				tset[hi.String()] = hi
			}
		}
		ts := make([]*big.Rat, 0, len(tset))
		for _, t := range tset {
			ts = append(ts, t)
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i].Cmp(ts[j]) < 0 })
		// Positive-length regions covered by >= 2 segments; merge
		// adjacent elementary intervals with identical participant sets.
		var pending *Overlap
		flush := func() {
			if pending != nil {
				overlaps = append(overlaps, *pending)
				pending = nil
			}
		}
		for i := 0; i+1 < len(ts); i++ {
			var ids []int
			for _, s := range ivs {
				if s.lo.Cmp(ts[i]) <= 0 && s.hi.Cmp(ts[i+1]) >= 0 {
					ids = append(ids, s.id)
				}
			}
			if len(ids) < 2 {
				flush()
				continue
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
		// Zero-length touches: positions where the common intersection
		// of the covering segments is exactly that point.
		for _, t := range ts {
			var cover []int
			var maxLo, minHi *big.Rat
			for _, s := range ivs {
				if s.lo.Cmp(t) <= 0 && s.hi.Cmp(t) >= 0 {
					cover = append(cover, s.id)
					if maxLo == nil || s.lo.Cmp(maxLo) > 0 {
						maxLo = s.lo
					}
					if minHi == nil || s.hi.Cmp(minHi) < 0 {
						minHi = s.hi
					}
				}
			}
			if len(cover) >= 2 && maxLo.Cmp(t) == 0 && minHi.Cmp(t) == 0 {
				sort.Ints(cover)
				record(linePointAt(rep, t), cover)
			}
		}
	}
	return overlaps
}

func equalInts(a, b []int) bool {
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
