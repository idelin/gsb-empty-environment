package intersect

import (
	"errors"
	"fmt"
)

// Verify checks a proof against the actual lists. It reads every block
// the proof touches: skipped blocks are re-hashed to confirm their
// summaries (and that the skip was justified), read blocks are
// re-intersected to confirm the result has no missing or extra items.
// Any tampering with summaries, evidence, or the result fails.
func Verify(lists [][]Block, proof Proof) error {
	if len(lists) == 0 {
		return errors.New("verify: no lists")
	}
	for i, l := range lists {
		if len(l) == 0 {
			return fmt.Errorf("verify: list %d has no blocks", i)
		}
	}

	// Pass 1: evidence must cover every block exactly once, and every
	// claimed summary must match the block's actual recomputed summary.
	type blockRef struct{ list, block int }
	seen := make(map[blockRef]bool)
	for _, ev := range proof.Evidence {
		if ev.ListIndex < 0 || ev.ListIndex >= len(lists) ||
			ev.BlockIndex < 0 || ev.BlockIndex >= len(lists[ev.ListIndex]) {
			return fmt.Errorf("verify: evidence references unknown block %d/%d", ev.ListIndex, ev.BlockIndex)
		}
		ref := blockRef{ev.ListIndex, ev.BlockIndex}
		if seen[ref] {
			return fmt.Errorf("verify: duplicate evidence for block %d/%d", ev.ListIndex, ev.BlockIndex)
		}
		seen[ref] = true
		blk := lists[ev.ListIndex][ev.BlockIndex]
		actual := Summarize(blk.IDs)
		if blk.Summary != actual {
			return fmt.Errorf("verify: block %d/%d stored summary does not match contents", ev.ListIndex, ev.BlockIndex)
		}
		if ev.Summary != actual {
			return fmt.Errorf("verify: block %d/%d evidence summary mismatch (tampered summary?)", ev.ListIndex, ev.BlockIndex)
		}
	}
	for i, l := range lists {
		for j := range l {
			if !seen[blockRef{i, j}] {
				return fmt.Errorf("verify: no evidence for block %d/%d (block hidden from proof)", i, j)
			}
		}
	}

	// Empty-list case: result must be empty and nothing may be read.
	emptyIdx := -1
	for i, l := range lists {
		if listCount(l) == 0 {
			emptyIdx = i
			break
		}
	}
	if emptyIdx >= 0 {
		if !proof.EmptyList {
			return errors.New("verify: a list is empty but proof does not declare it")
		}
		if len(proof.Result) != 0 {
			return errors.New("verify: non-empty result despite empty list")
		}
		for _, ev := range proof.Evidence {
			if ev.Disposition == Read {
				return fmt.Errorf("verify: block %d/%d claimed read although list %d is empty", ev.ListIndex, ev.BlockIndex, emptyIdx)
			}
		}
		return nil
	}
	if proof.EmptyList {
		return errors.New("verify: proof claims an empty list but none is empty")
	}

	// Pass 2: recompute the feasibility window and check every
	// disposition is canonical and justified.
	lower, upper := bounds(lists)
	if proof.Lower != lower || proof.Upper != upper {
		return fmt.Errorf("verify: proof bounds [%d,%d] do not match recomputed [%d,%d]",
			proof.Lower, proof.Upper, lower, upper)
	}
	readIDs := make([][]uint64, len(lists))
	for _, ev := range proof.Evidence {
		s := ev.Summary
		switch ev.Disposition {
		case SkippedEmptyBlock:
			if s.Count != 0 {
				return fmt.Errorf("verify: block %d/%d claimed empty but count is %d", ev.ListIndex, ev.BlockIndex, s.Count)
			}
		case SkippedBelow:
			if !(s.Count > 0 && s.Max < lower) {
				return fmt.Errorf("verify: block %d/%d skip-below unjustified (max %d, lower %d)", ev.ListIndex, ev.BlockIndex, s.Max, lower)
			}
		case SkippedAbove:
			if !(s.Count > 0 && s.Min > upper) {
				return fmt.Errorf("verify: block %d/%d skip-above unjustified (min %d, upper %d)", ev.ListIndex, ev.BlockIndex, s.Min, upper)
			}
		case Read:
			if s.Count == 0 || s.Max < lower || s.Min > upper {
				return fmt.Errorf("verify: block %d/%d was read but should have been skipped", ev.ListIndex, ev.BlockIndex)
			}
			readIDs[ev.ListIndex] = append(readIDs[ev.ListIndex], lists[ev.ListIndex][ev.BlockIndex].IDs...)
		default:
			return fmt.Errorf("verify: block %d/%d has unknown disposition", ev.ListIndex, ev.BlockIndex)
		}
	}

	// Pass 3: recompute the intersection from the read blocks and
	// compare with the claimed result.
	want := intersectSorted(readIDs)
	if !equalIDs(want, proof.Result) {
		return fmt.Errorf("verify: result mismatch: recomputed %v, proof claims %v", want, proof.Result)
	}
	for i, id := range proof.Result {
		if i > 0 && id <= proof.Result[i-1] {
			return errors.New("verify: result not strictly increasing")
		}
		if id < lower || id > upper {
			return fmt.Errorf("verify: result id %d outside feasible window [%d,%d]", id, lower, upper)
		}
	}
	return nil
}

func equalIDs(a, b []uint64) bool {
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
