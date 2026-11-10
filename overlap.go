package main

import "sort"

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func (g *groupAccumulator) buildOverlaps() []overlapReport {
	ordered := append([]*decodedFragment(nil), g.fragments...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].absStart != ordered[j].absStart {
			return ordered[i].absStart < ordered[j].absStart
		}
		return ordered[i].frag.ID < ordered[j].frag.ID
	})

	overlaps := make([]overlapReport, 0)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			a, b := ordered[i], ordered[j]
			lo := maxUint64(a.absStart, b.absStart)
			hi := minUint64(a.absEnd, b.absEnd)
			if hi <= lo {
				continue
			}
			overlaps = append(overlaps, overlapReport{
				FragmentA:     a.frag.ID,
				FragmentB:     b.frag.ID,
				StartBlock:    lo / blockSize,
				EndBlock:      (hi - 1) / blockSize,
				StartPosition: lo,
				EndPosition:   hi,
				OverlapBytes:  int(hi - lo),
			})
		}
	}

	sort.Slice(overlaps, func(i, j int) bool {
		if overlaps[i].StartPosition != overlaps[j].StartPosition {
			return overlaps[i].StartPosition < overlaps[j].StartPosition
		}
		if overlaps[i].FragmentA != overlaps[j].FragmentA {
			return overlaps[i].FragmentA < overlaps[j].FragmentA
		}
		return overlaps[i].FragmentB < overlaps[j].FragmentB
	})
	return overlaps
}
