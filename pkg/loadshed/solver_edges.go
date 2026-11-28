package loadshed

import (
	"errors"
	"sort"
)

var errNilGraph = errors.New("loadshed: nil graph")

// pickSoftEdge 在当前未切的 SOFT 边中挑选对本档瓶颈最“划算”的一条。
// 排序键：单位缓解量代价 cost/gain 升序 -> 缓解量 gain 降序 -> 边编号升序，保证结果确定。
// 仅考虑能严格提升本档保留量的边；返回 ok=false 表示没有边能缓解当前瓶颈。
func pickSoftEdge(g *Graph, cut map[int]bool, p Priority, locked [][NumPriorities]float64, curAdm Admission) (int, bool) {
	type cand struct {
		edgeIdx int
		cost    float64
		gain    float64
	}
	baseKept := 0.0
	for v, nd := range g.Nodes {
		baseKept += nd.Arrival[p] * curAdm[v][p]
	}
	cands := make([]cand, 0)
	for ei, e := range g.Edges {
		if e.Dep != Soft || cut[ei] {
			continue
		}
		trial := make(map[int]bool, len(cut)+1)
		for k := range cut {
			trial[k] = true
		}
		trial[ei] = true
		routing, err := g.BuildRouting(cutScales(trial))
		if err != nil {
			continue
		}
		_, kept := runTier(g, routing, p, locked)
		gain := kept - baseKept
		if gain <= Eps {
			continue
		}
		cands = append(cands, cand{edgeIdx: ei, cost: e.DegradeCost, gain: gain})
	}
	if len(cands) == 0 {
		return 0, false
	}
	sort.Slice(cands, func(i, j int) bool {
		ri := cands[i].cost / cands[i].gain
		rj := cands[j].cost / cands[j].gain
		if !approxEQ(ri, rj) {
			return ri < rj
		}
		if !approxEQ(cands[i].gain, cands[j].gain) {
			return cands[i].gain > cands[j].gain
		}
		return cands[i].edgeIdx < cands[j].edgeIdx
	})
	return cands[0].edgeIdx, true
}

// pruneRedundantCuts 按降级代价从高到低逐条尝试恢复被切边。
// 恢复后重新做全档求解，只有当四档保留量的字典序不劣化时才接受恢复，
// 从而在不牺牲任何高优流量的前提下，得到代价最小的切边集合。
func pruneRedundantCuts(g *Graph, cut map[int]bool) map[int]bool {
	if len(cut) == 0 {
		return cut
	}
	edgeIDs := make([]int, 0, len(cut))
	for ei := range cut {
		edgeIDs = append(edgeIDs, ei)
	}
	sort.Slice(edgeIDs, func(i, j int) bool {
		ci, cj := g.Edges[edgeIDs[i]].DegradeCost, g.Edges[edgeIDs[j]].DegradeCost
		if !approxEQ(ci, cj) {
			return ci > cj
		}
		return edgeIDs[i] < edgeIDs[j]
	})

	baseline := retainedVector(g, cut)
	for _, ei := range edgeIDs {
		trial := make(map[int]bool, len(cut))
		for k := range cut {
			if k != ei {
				trial[k] = true
			}
		}
		cand := retainedVector(g, trial)
		// 只有恢复后四档保留量在字典序上严格劣化时才保留该切边；
		// 相等（不劣化）说明它是冗余降级，应当恢复，从而最小化降级代价。
		if lexicographicallyLess(cand, baseline) {
			continue
		}
		{
			cut = trial
			baseline = cand
		}
	}
	return cut
}

// retainedVector 在给定切边集合下做全档求解，返回四档保留量，用于字典序比较。
func retainedVector(g *Graph, cut map[int]bool) [NumPriorities]float64 {
	routing, err := g.BuildRouting(cutScales(cut))
	if err != nil {
		var zero [NumPriorities]float64
		return zero
	}
	_, retained := runAllTiers(g, routing)
	return retained
}

// lexicographicallyLess 判断 a 是否在四档保留量字典序上严格劣于 b
// （任一档 b 更高且更高档完全相等）。相等或 a 更优时返回 false。
func lexicographicallyLess(a, b [NumPriorities]float64) bool {
	for p := Priority(0); p < NumPriorities; p++ {
		if approxEQ(a[p], b[p]) {
			continue
		}
		return a[p] < b[p]
	}
	return false
}
