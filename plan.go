package taskpkg

import "sort"

func buildPlan(oldInfo, newInfo *treeInfo, base [][]int, assign []int, opts Options, useIDs bool, stats Stats) Plan {
	nO, nN := len(oldInfo.active), len(newInfo.active)
	if nO == 0 {
		assign = nil
	}

	usedNew := make([]bool, nN)
	for _, j := range assign {
		if j >= 0 && j < nN {
			usedNew[j] = true
		}
	}

	ops := make([]Operation, 0, nO+nN)
	total := 0

	for i := 0; i < nO; i++ {
		ob := oldInfo.active[i]
		if assign[i] < 0 {
			op := Operation{
				Kind:           OpDelete,
				OldOrder:       i,
				NewOrder:       -1,
				OldID:          ob.block.ID,
				OldRole:        ob.block.Role,
				OldText:        ob.block.Text,
				OldParentID:    ob.parentID(),
				OldParentOrder: ob.activeParent,
				NewParentOrder: -1,
				Cost:           opts.DeleteCost,
				OldSpan:        ob.span,
			}
			op.OriginalIntervals = []TextSpan{ob.span}
			ops = append(ops, op)
			total += op.Cost
			stats.Deleted++
			continue
		}

		j := assign[i]
		nb := newInfo.active[j]
		moved := parentMovedForAssign(oldInfo, newInfo, assign, i, j, useIDs, true)
		rolePenalty := 0
		if ob.block.Role != nb.block.Role {
			rolePenalty = opts.RolePenalty
		}
		movePenalty := 0
		kind := OpMatch
		if moved {
			kind = OpMove
			movePenalty = opts.MoveCost
			stats.Moved++
		}
		stats.Matched++
		cost := base[i][j] + movePenalty
		op := Operation{
			Kind:           kind,
			OldOrder:       i,
			NewOrder:       j,
			OldID:          ob.block.ID,
			NewID:          nb.block.ID,
			OldRole:        ob.block.Role,
			NewRole:        nb.block.Role,
			OldText:        ob.block.Text,
			NewText:        nb.block.Text,
			OldParentID:    ob.parentID(),
			NewParentID:    nb.parentID(),
			OldParentOrder: ob.activeParent,
			NewParentOrder: nb.activeParent,
			Cost:           cost,
			TextDistance:   levenshteinRunes(ob.runes, nb.runes),
			RolePenalty:    rolePenalty,
			MovePenalty:    movePenalty,
			Moved:          moved,
			OldSpan:        ob.span,
			NewSpan:        nb.span,
		}
		op.OriginalIntervals, op.NewIntervals = changedIntervals(ob, nb)
		ops = append(ops, op)
		total += cost
	}

	for j := 0; j < nN; j++ {
		if usedNew[j] {
			continue
		}
		nb := newInfo.active[j]
		op := Operation{
			Kind:           OpInsert,
			OldOrder:       -1,
			NewOrder:       j,
			NewID:          nb.block.ID,
			NewRole:        nb.block.Role,
			NewText:        nb.block.Text,
			NewParentID:    nb.parentID(),
			OldParentOrder: -1,
			NewParentOrder: nb.activeParent,
			Cost:           opts.InsertCost,
			NewSpan:        nb.span,
		}
		ops = append(ops, op)
		total += op.Cost
		stats.Inserted++
	}

	return Plan{
		Operations: ops,
		TotalCost:  total,
		Stats:      stats,
	}
}

func changedIntervals(oldBlock, newBlock *blockInfo) ([]TextSpan, []TextSpan) {
	if oldBlock.block.Text == newBlock.block.Text {
		return nil, nil
	}

	oldSpans, newSpans := alignmentChangedSpans(oldBlock, newBlock)
	if len(oldSpans) > 0 || len(newSpans) > 0 {
		return oldSpans, newSpans
	}

	// Fallback for very large pairs: one conservative interval covering the
	// non-common prefix/suffix region.
	prefix := 0
	for prefix < len(oldBlock.runes) && prefix < len(newBlock.runes) &&
		oldBlock.runes[prefix] == newBlock.runes[prefix] {
		prefix++
	}
	oldEnd, newEnd := len(oldBlock.runes), len(newBlock.runes)
	for oldEnd > prefix && newEnd > prefix &&
		oldBlock.runes[oldEnd-1] == newBlock.runes[newEnd-1] {
		oldEnd--
		newEnd--
	}

	oldChanged := oldBlock.localSpan(prefix, oldEnd)
	newChanged := newBlock.localSpan(prefix, newEnd)
	return []TextSpan{oldChanged}, []TextSpan{newChanged}
}

const maxAlignmentCells = 1_000_000

func alignmentChangedSpans(oldBlock, newBlock *blockInfo) ([]TextSpan, []TextSpan) {
	a, b := oldBlock.runes, newBlock.runes
	if len(a)*len(b) > maxAlignmentCells {
		return nil, nil
	}

	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
		dp[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		dp[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := dp[i-1][j-1]
			if a[i-1] != b[j-1] {
				cost++
			}
			if v := dp[i-1][j] + 1; v < cost {
				cost = v
			}
			if v := dp[i][j-1] + 1; v < cost {
				cost = v
			}
			dp[i][j] = cost
		}
	}

	oldMark := make([]bool, len(a))
	newMark := make([]bool, len(b))
	oldGaps := make([]int, 0)
	newGaps := make([]int, 0)
	i, j := len(a), len(b)
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1] && dp[i][j] == dp[i-1][j-1]:
			i--
			j--
		case i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+1:
			oldMark[i-1] = true
			newMark[j-1] = true
			i--
			j--
		case i > 0 && dp[i][j] == dp[i-1][j]+1:
			oldMark[i-1] = true
			newGaps = append(newGaps, j)
			i--
		case j > 0 && dp[i][j] == dp[i][j-1]+1:
			newMark[j-1] = true
			oldGaps = append(oldGaps, i)
			j--
		default:
			// Defensive fallback; the DP path above is always available.
			if i > 0 {
				oldMark[i-1] = true
				i--
			} else if j > 0 {
				newMark[j-1] = true
				j--
			}
		}
	}

	oldSpans := markedSpans(oldBlock, oldMark, oldGaps)
	newSpans := markedSpans(newBlock, newMark, newGaps)
	return oldSpans, newSpans
}

func markedSpans(b *blockInfo, marked []bool, gaps []int) []TextSpan {
	var spans []TextSpan
	for i := 0; i < len(marked); {
		if !marked[i] {
			i++
			continue
		}
		start := i
		for i < len(marked) && marked[i] {
			i++
		}
		spans = append(spans, b.localSpan(start, i))
	}
	for _, gap := range gaps {
		covered := false
		for _, s := range spans {
			localStart := s.RuneStart - b.span.RuneStart
			localEnd := s.RuneEnd - b.span.RuneStart
			if gap >= localStart && gap <= localEnd {
				covered = true
				break
			}
		}
		if !covered {
			spans = append(spans, b.localSpan(gap, gap))
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].RuneStart != spans[j].RuneStart {
			return spans[i].RuneStart < spans[j].RuneStart
		}
		return spans[i].RuneEnd < spans[j].RuneEnd
	})
	return spans
}
