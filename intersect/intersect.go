package intersect

import (
	"errors"
	"fmt"
)

// Disposition records what the prover did with a block.
type Disposition int

const (
	// Read means the block's contents were inspected. Range overlap
	// alone never implies a hit; overlapping blocks are always read.
	Read Disposition = iota
	// SkippedEmptyBlock: block contains no IDs.
	SkippedEmptyBlock
	// SkippedBelow: block max < global lower bound.
	SkippedBelow
	// SkippedAbove: block min > global upper bound.
	SkippedAbove
	// SkippedEmptyList: some list is empty, so the intersection is
	// trivially empty and no block needs reading.
	SkippedEmptyList
)

func (d Disposition) String() string {
	switch d {
	case Read:
		return "read"
	case SkippedEmptyBlock:
		return "skipped-empty-block"
	case SkippedBelow:
		return "skipped-below"
	case SkippedAbove:
		return "skipped-above"
	case SkippedEmptyList:
		return "skipped-empty-list"
	}
	return "unknown"
}

// Evidence is the per-block proof entry: the claimed summary, what was
// done with the block, and the concrete justification for any skip.
type Evidence struct {
	ListIndex   int
	BlockIndex  int
	Summary     Summary
	Disposition Disposition
	Reason      string
}

// Proof binds the intersection result to per-block evidence.
type Proof struct {
	// Lower/Upper are the global feasibility bounds: any common ID x
	// satisfies Lower <= x <= Upper because x must lie in every list.
	Lower, Upper uint64
	// EmptyList is true when some list is empty; the result is then
	// trivially empty and all blocks are skipped.
	EmptyList bool
	Result    []uint64
	Evidence  []Evidence
}

func listCount(list []Block) int {
	n := 0
	for _, b := range list {
		n += b.Summary.Count
	}
	return n
}

// intersectSorted intersects k sorted, duplicate-free slices.
func intersectSorted(sets [][]uint64) []uint64 {
	if len(sets) == 0 {
		return nil
	}
	acc := sets[0]
	for _, s := range sets[1:] {
		acc = intersectTwo(acc, s)
		if len(acc) == 0 {
			return nil
		}
	}
	return acc
}

func intersectTwo(a, b []uint64) []uint64 {
	var out []uint64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

// bounds computes the global feasibility window [lower, upper] from
// per-list min/max. Only valid when no list is empty.
func bounds(lists [][]Block) (lower, upper uint64) {
	lower = lists[0][0].Summary.Min
	upper = lists[0][len(lists[0])-1].Summary.Max
	for _, l := range lists {
		if m := l[0].Summary.Min; m > lower {
			lower = m
		}
		if m := l[len(l)-1].Summary.Max; m < upper {
			upper = m
		}
	}
	return lower, upper
}

// Intersect computes the intersection of the given lists and returns a
// proof. Blocks whose range cannot contain any common ID are skipped
// with a recorded justification; every other block is read.
func Intersect(lists [][]Block) ([]uint64, Proof, error) {
	if len(lists) == 0 {
		return nil, Proof{}, errors.New("intersect: no lists")
	}
	for i, l := range lists {
		if err := validateList(l); err != nil {
			return nil, Proof{}, fmt.Errorf("intersect: list %d: %v", i, err)
		}
		if len(l) == 0 {
			return nil, Proof{}, fmt.Errorf("intersect: list %d has no blocks", i)
		}
	}
	var proof Proof

	emptyIdx := -1
	for i, l := range lists {
		if listCount(l) == 0 {
			emptyIdx = i
			break
		}
	}
	if emptyIdx >= 0 {
		proof.EmptyList = true
		for i, l := range lists {
			for j, b := range l {
				proof.Evidence = append(proof.Evidence, Evidence{
					ListIndex:   i,
					BlockIndex:  j,
					Summary:     b.Summary,
					Disposition: SkippedEmptyList,
					Reason:      fmt.Sprintf("list %d is empty, so the intersection is empty; no block needs reading", emptyIdx),
				})
			}
		}
		return nil, proof, nil
	}

	lower, upper := bounds(lists)
	proof.Lower, proof.Upper = lower, upper

	readIDs := make([][]uint64, len(lists))
	for i, l := range lists {
		for j, b := range l {
			ev := Evidence{ListIndex: i, BlockIndex: j, Summary: b.Summary}
			s := b.Summary
			switch {
			case s.Count == 0:
				ev.Disposition = SkippedEmptyBlock
				ev.Reason = "block is empty"
			case s.Max < lower:
				ev.Disposition = SkippedBelow
				ev.Reason = fmt.Sprintf("block max %d < lower bound %d: every common ID must be >= %d", s.Max, lower, lower)
			case s.Min > upper:
				ev.Disposition = SkippedAbove
				ev.Reason = fmt.Sprintf("block min %d > upper bound %d: every common ID must be <= %d", s.Min, upper, upper)
			default:
				ev.Disposition = Read
				ev.Reason = fmt.Sprintf("range [%d,%d] overlaps [%d,%d]; overlap does not imply hits, contents inspected", s.Min, s.Max, lower, upper)
				readIDs[i] = append(readIDs[i], b.IDs...)
			}
			proof.Evidence = append(proof.Evidence, ev)
		}
	}

	proof.Result = intersectSorted(readIDs)
	return proof.Result, proof, nil
}
