package readproof

import (
	"fmt"
	"sort"
)

type validationIndex struct {
	byBlock map[uint64][]PhysicalRange
}

func newValidationIndex(ranges []PhysicalRange) (*validationIndex, error) {
	sorted := append([]PhysicalRange(nil), ranges...)
	for _, r := range sorted {
		if _, ok := r.End(); !ok {
			return nil, fmt.Errorf("%w: validated physical interval", ErrLengthOverflow)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Block != sorted[j].Block {
			return sorted[i].Block < sorted[j].Block
		}
		if sorted[i].Offset != sorted[j].Offset {
			return sorted[i].Offset < sorted[j].Offset
		}
		return sorted[i].Length < sorted[j].Length
	})

	idx := &validationIndex{byBlock: make(map[uint64][]PhysicalRange)}
	for _, r := range sorted {
		if r.Length == 0 {
			continue
		}
		block := idx.byBlock[r.Block]
		if len(block) == 0 {
			idx.byBlock[r.Block] = append(block, r)
			continue
		}
		last := &block[len(block)-1]
		lastEnd, _ := last.End()
		// Adjacent intervals are merged as well as overlapping ones.
		if r.Offset <= lastEnd {
			rEnd, _ := r.End()
			if rEnd > lastEnd {
				last.Length = rEnd - last.Offset
			}
			idx.byBlock[r.Block] = block
			continue
		}
		idx.byBlock[r.Block] = append(block, r)
	}
	return idx, nil
}

// containingPoint returns the merged validated interval containing off in
// block.  It returns false at the half-open end of an interval.
func (v *validationIndex) containingPoint(block, off uint64) (PhysicalRange, bool) {
	intervals := v.byBlock[block]
	i := sort.Search(len(intervals), func(i int) bool {
		end, _ := intervals[i].End()
		return end > off
	})
	if i == len(intervals) {
		return PhysicalRange{}, false
	}
	r := intervals[i]
	if r.Offset > off {
		return PhysicalRange{}, false
	}
	return r, true
}

// containsRange reports whether every byte of want is in one merged validated
// interval.
func (v *validationIndex) containsRange(want PhysicalRange) bool {
	end, ok := want.End()
	if !ok || want.Length == 0 {
		return false
	}
	interval, ok := v.containingPoint(want.Block, want.Offset)
	if !ok {
		return false
	}
	intervalEnd, _ := interval.End()
	return intervalEnd >= end
}
