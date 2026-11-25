package taskpkg

import (
	"fmt"
	"sort"
)

type verticalBand struct {
	top    float64
	bottom float64
	blocks []int
}

func (e *engine) buildResult(best evaluatedPartition, partitions []Partition) *Result {
	selected := append([]int(nil), best.lineIdx...)
	sort.Slice(selected, func(i, j int) bool {
		li := e.lines[selected[i]]
		lj := e.lines[selected[j]]
		if li.X != lj.X {
			return li.X < lj.X
		}
		return li.ID < lj.ID
	})

	result := &Result{
		Partitions:      partitions,
		SelectedMask:    best.partition.Mask,
		SelectedLineIDs: make([]string, 0, len(selected)),
	}
	for _, li := range selected {
		result.SelectedLineIDs = append(result.SelectedLineIDs, e.lines[li].ID)
	}

	crossedIndices := make([]int, 0, len(best.crossings))
	for bi := range best.crossings {
		crossedIndices = append(crossedIndices, bi)
	}
	sort.Ints(crossedIndices)

	conflicts := make([]Conflict, 0)
	for _, bi := range crossedIndices {
		id := e.public[bi]
		result.CrossedBlocks = append(result.CrossedBlocks, id)
		if e.blocks[bi].Heading {
			result.CrossedHeadingBlocks = append(result.CrossedHeadingBlocks, id)
		} else {
			result.CrossedNonHeadingBlocks = append(result.CrossedNonHeadingBlocks, id)
		}

		for _, li := range best.crossings[bi] {
			line := e.lines[li]
			if e.blocks[bi].Heading {
				conflicts = append(conflicts, Conflict{
					Code:     "cross_column_heading",
					Severity: SeverityInfo,
					BlockID:  id,
					LineID:   line.ID,
					X:        line.X,
					Reason:   fmt.Sprintf("栏线 %s 穿过显式标题块 %s；标题豁免惩罚并按跨栏块处理", line.ID, id),
				})
			} else {
				conflicts = append(conflicts, Conflict{
					Code:     "divider_crosses_non_heading",
					Severity: SeverityWarning,
					BlockID:  id,
					LineID:   line.ID,
					X:        line.X,
					Reason:   fmt.Sprintf("栏线 %s 穿过非标题块 %s；该块计入分区冲突", line.ID, id),
				})
			}
		}
	}

	columnCount := len(selected) + 1
	columnIndices := make([][]int, columnCount)
	var spanning []int
	for bi := range e.blocks {
		if len(best.crossings[bi]) > 0 {
			spanning = append(spanning, bi)
			continue
		}
		col := e.columnForBlock(bi, selected)
		columnIndices[col] = append(columnIndices[col], bi)
	}

	for ci := range columnIndices {
		columnIndices[ci] = e.sortVerticalStructural(columnIndices[ci])
		result.Columns = append(result.Columns, Column{
			Index: ci,
			IDs:   e.publicIDs(columnIndices[ci]),
		})
	}
	spanning = e.sortVerticalStructural(spanning)

	bands := e.buildBands(spanning)
	regionOf := make([]int, len(e.blocks))
	for i := range regionOf {
		regionOf[i] = -1
	}
	for ci, indices := range columnIndices {
		_ = ci
		for _, bi := range indices {
			regionOf[bi] = e.regionForBlock(bi, bands)
		}
	}

	columnFlow := make([]int, columnCount)
	for i := range columnFlow {
		columnFlow[i] = i
	}
	if e.dir == DirectionRTL {
		for left, right := 0, len(columnFlow)-1; left < right; left, right = left+1, right-1 {
			columnFlow[left], columnFlow[right] = columnFlow[right], columnFlow[left]
		}
	}

	appendRegion := func(region int) {
		for _, ci := range columnFlow {
			for _, bi := range columnIndices[ci] {
				if regionOf[bi] == region {
					result.Order = append(result.Order, e.public[bi])
				}
			}
		}
	}

	appendRegion(0)
	for bandIndex, band := range bands {
		orderedBand := e.sortInsideOverlap(append([]int(nil), band.blocks...))
		for _, bi := range orderedBand {
			result.Order = append(result.Order, e.public[bi])
		}
		appendRegion(bandIndex + 1)
	}

	for _, indices := range columnIndices {
		conflicts = e.appendOrderConflicts(conflicts, indices)
	}
	conflicts = e.appendOrderConflicts(conflicts, spanning)

	result.Conflicts = conflicts
	result.Reasons = make([]string, len(conflicts))
	for i, conflict := range conflicts {
		result.Reasons[i] = conflict.Reason
	}
	return result
}

func (e *engine) columnForBlock(blockIndex int, selectedLineIndices []int) int {
	r := e.blocks[blockIndex].Rect
	column := 0
	for _, li := range selectedLineIndices {
		x := e.lines[li].X
		// A line exactly on an edge is a boundary, not an intersection.
		if x <= r.Left() {
			column++
		}
	}
	return column
}

func (e *engine) publicIDs(indices []int) []string {
	out := make([]string, 0, len(indices))
	for _, i := range indices {
		out = append(out, e.public[i])
	}
	return out
}

// sortVerticalStructural groups connected vertically-overlapping blocks.
// Disjoint groups have an unambiguous vertical order. Inside an overlap group
// (typical for floats/annotations), explicit DOM ancestry/order decides before
// arbitrary coordinates or IDs.
func (e *engine) sortVerticalStructural(indices []int) []int {
	if len(indices) < 2 {
		return append([]int(nil), indices...)
	}

	ordered := append([]int(nil), indices...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a := e.blocks[ordered[i]].Rect
		b := e.blocks[ordered[j]].Rect
		if a.Top() != b.Top() {
			return a.Top() < b.Top()
		}
		return e.tieIDs[ordered[i]] < e.tieIDs[ordered[j]]
	})

	groups := make([][]int, 0)
	maxBottom := 0.0
	for _, bi := range ordered {
		r := e.blocks[bi].Rect
		if len(groups) == 0 || r.Top() >= maxBottom {
			groups = append(groups, []int{bi})
			maxBottom = r.Bottom()
			continue
		}
		last := len(groups) - 1
		groups[last] = append(groups[last], bi)
		if r.Bottom() > maxBottom {
			maxBottom = r.Bottom()
		}
	}

	out := make([]int, 0, len(ordered))
	for _, group := range groups {
		out = append(out, e.sortInsideOverlap(group)...)
	}
	return out
}

func (e *engine) sortInsideOverlap(indices []int) []int {
	sort.SliceStable(indices, func(i, j int) bool {
		a := indices[i]
		b := indices[j]
		if cmp, explicit := e.compareExplicitStructure(a, b); explicit && cmp != 0 {
			return cmp < 0
		}

		ra := e.blocks[a].Rect
		rb := e.blocks[b].Rect
		if ra.Top() != rb.Top() {
			return ra.Top() < rb.Top()
		}
		if ra.Bottom() != rb.Bottom() {
			return ra.Bottom() < rb.Bottom()
		}
		return e.tieIDs[a] < e.tieIDs[b]
	})
	return indices
}

func (e *engine) compareExplicitStructure(a, b int) (int, bool) {
	pa := e.paths[a]
	pb := e.paths[b]
	common := len(pa)
	if len(pb) < common {
		common = len(pb)
	}

	for i := 0; i < common; i++ {
		if pa[i] == pb[i] {
			continue
		}
		oa, oka := e.orders[pa[i]]
		ob, okb := e.orders[pb[i]]
		if oka && okb && oa != ob {
			return cmpInt(oa, ob), true
		}
		break
	}

	if common == len(pa) && common == len(pb) {
		// Same normalized path; fall through to stable ID handling.
	} else if common == len(pa) {
		return -1, true
	} else if common == len(pb) {
		return 1, true
	}

	ida := e.public[a]
	idb := e.public[b]
	oa, oka := e.orders[ida]
	ob, okb := e.orders[idb]
	if !oka {
		oa, oka = e.orders[e.tieIDs[a]]
	}
	if !okb {
		ob, okb = e.orders[e.tieIDs[b]]
	}
	if oka && okb && oa != ob {
		return cmpInt(oa, ob), true
	}

	return 0, false
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func (e *engine) buildBands(spanning []int) []*verticalBand {
	topSorted := append([]int(nil), spanning...)
	sort.SliceStable(topSorted, func(i, j int) bool {
		a := e.blocks[topSorted[i]].Rect
		b := e.blocks[topSorted[j]].Rect
		if a.Top() != b.Top() {
			return a.Top() < b.Top()
		}
		return e.tieIDs[topSorted[i]] < e.tieIDs[topSorted[j]]
	})

	bands := make([]*verticalBand, 0)
	for _, bi := range topSorted {
		r := e.blocks[bi].Rect
		if len(bands) == 0 || r.Top() >= bands[len(bands)-1].bottom {
			bands = append(bands, &verticalBand{top: r.Top(), bottom: r.Bottom(), blocks: []int{bi}})
			continue
		}
		last := bands[len(bands)-1]
		last.blocks = append(last.blocks, bi)
		if r.Bottom() > last.bottom {
			last.bottom = r.Bottom()
		}
		if r.Top() < last.top {
			last.top = r.Top()
		}
	}
	return bands
}

func (e *engine) regionForBlock(bi int, bands []*verticalBand) int {
	if len(bands) == 0 {
		return 0
	}

	r := e.blocks[bi].Rect
	region := len(bands)
	overlapBand := -1
	for gi, band := range bands {
		if r.Bottom() <= band.top {
			region = gi
			break
		}
		if verticalIntervalsOverlap(r.Top(), r.Bottom(), band.top, band.bottom) && overlapBand == -1 {
			overlapBand = gi
		}
	}

	if overlapBand == -1 {
		return region
	}

	band := bands[overlapBand]
	for _, other := range band.blocks {
		if cmp, explicit := e.compareExplicitStructure(bi, other); explicit && cmp != 0 {
			if cmp < 0 {
				return overlapBand
			}
			return overlapBand + 1
		}
	}

	midY := (band.top + band.bottom) / 2
	if r.CenterY() < midY || (r.CenterY() == midY && r.Top() < band.top) {
		return overlapBand
	}
	return overlapBand + 1
}

func verticalIntervalsOverlap(aTop, aBottom, bTop, bBottom float64) bool {
	return aTop < bBottom && bTop < aBottom
}

func rectsVerticallyOverlap(a, b Rect) bool {
	return verticalIntervalsOverlap(a.Top(), a.Bottom(), b.Top(), b.Bottom())
}

func (e *engine) appendOrderConflicts(conflicts []Conflict, ordered []int) []Conflict {
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			a := ordered[i]
			b := ordered[j]
			ra := e.blocks[a].Rect
			rb := e.blocks[b].Rect
			structCmp, explicit := e.compareExplicitStructure(a, b)
			overlap := rectsVerticallyOverlap(ra, rb)

			if overlap {
				topCmp := cmpFloat(ra.Top(), rb.Top())
				if explicit && structCmp != 0 && topCmp != 0 && structCmp != topCmp {
					conflicts = append(conflicts, Conflict{
						Code:         "structure_resolves_overlap",
						Severity:     SeverityInfo,
						BlockID:      e.public[a],
						OtherBlockID: e.public[b],
						Reason: fmt.Sprintf(
							"块 %s 与 %s 纵向矩形相交且坐标先后和明确祖先顺序不一致；按结构约束将 %s 置于前面",
							e.public[a], e.public[b], e.public[a],
						),
					})
				} else if !explicit || structCmp == 0 {
					if ra.Top() == rb.Top() && ra.Bottom() == rb.Bottom() {
						conflicts = append(conflicts, e.idTieConflict(a, b))
					} else {
						conflicts = append(conflicts, Conflict{
							Code:         "vertical_overlap_without_structure",
							Severity:     SeverityInfo,
							BlockID:      e.public[a],
							OtherBlockID: e.public[b],
							Reason: fmt.Sprintf(
								"块 %s 与 %s 为重叠/悬浮矩形且无明确祖先先后；使用纵向坐标和原节点 ID 确定性定序",
								e.public[a], e.public[b],
							),
						})
					}
				}
				continue
			}

			geometricCmp := 0
			if ra.Bottom() <= rb.Top() {
				geometricCmp = -1
			} else if rb.Bottom() <= ra.Top() {
				geometricCmp = 1
			}
			if explicit && structCmp != 0 && geometricCmp != 0 && structCmp != geometricCmp {
				conflicts = append(conflicts, Conflict{
					Code:         "vertical_ancestor_order_conflict",
					Severity:     SeverityWarning,
					BlockID:      e.public[a],
					OtherBlockID: e.public[b],
					Reason: fmt.Sprintf(
						"块 %s 与 %s 纵向不相交，但纵向顺序与明确 DOM 祖先/节点顺序相反；默认保留栏内纵向顺序并标记结构冲突",
						e.public[a], e.public[b],
					),
				})
			}

			if ra.Top() == rb.Top() && ra.Bottom() == rb.Bottom() && (!explicit || structCmp == 0) {
				conflicts = append(conflicts, e.idTieConflict(a, b))
			}
		}
	}
	return conflicts
}

func (e *engine) idTieConflict(a, b int) Conflict {
	first := e.tieIDs[a]
	second := e.tieIDs[b]
	if first > second {
		first, second = second, first
	}
	return Conflict{
		Code:         "original_node_id_tie_break",
		Severity:     SeverityInfo,
		BlockID:      e.public[a],
		OtherBlockID: e.public[b],
		Reason:       fmt.Sprintf("块 %s 与 %s 的纵向和结构键平局；按原节点 ID %s < %s 定序", e.public[a], e.public[b], first, second),
	}
}
