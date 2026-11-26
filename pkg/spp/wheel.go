package spp

import (
	"fmt"
	"sort"
)

// DefaultWheelLimit 限制单次分析输出的争议轮数量。
const DefaultWheelLimit = 16

// DisputeWheel 是 Griffin-Wilfong 定义的争议轮：
//
//	U = (u_0, ..., u_{k-1}) 为 Pivot AS 环；
//	Q_i（直接路径/辐条）是 pivot u_i 的允许路径，且不经过任何其他 pivot；
//	R_i（轮缘路径）是 u_i 的允许路径且以环上邻居 u_{i+1} 开头；
//	对每个 i 都有 R_i >_{u_i} Q_i，形成闭环的循环偏好。
type DisputeWheel struct {
	// Pivots 为 Pivot 环 u_0..u_{k-1}，且有下一跳边 u_i -> u_{i+1}
	// （下标模 k）。
	Pivots []int `json:"pivots"`
	// Spokes 即各 pivot 的直接路径 Q_i。
	Spokes []Path `json:"spokes"`
	// Rims 即经环上邻居扩展的更优路径 R_i。
	Rims []Path `json:"rims"`
	// SpokeRanks 与 RimRanks 给出 Q_i、R_i 在对应 pivot 偏好序中的排名。
	SpokeRanks []int `json:"spokeRanks"`
	RimRanks   []int `json:"rimRanks"`
	// Proof 逐条给出 R_i >_{u_i} Q_i 的可机读证明文本。
	Proof []string `json:"proof"`
}

// Size 返回争议轮的 pivot 数量。
func (w *DisputeWheel) Size() int { return len(w.Pivots) }

func (g *Graph) nextHopMap() map[int]map[int]bool {
	out := make(map[int]map[int]bool)
	for _, as := range g.SortedNodes() {
		if as == g.Destination {
			continue
		}
		out[as] = make(map[int]bool)
		for _, hop := range g.NextHops(as) {
			out[as][hop] = true
		}
	}
	return out
}

// canonicalCycles 枚举“环上最小 pivot 必为起点”的全部简单有向环，
// 保证每个环只输出一次，环长 >= 2。
func canonicalCycles(g *Graph, hops map[int]map[int]bool) [][]int {
	var cycles [][]int
	vertices := g.SortedNodes()
	for _, start := range vertices {
		if start == g.Destination {
			continue
		}
		visited := map[int]bool{start: true}
		var dfs func(cur int, path []int)
		dfs = func(cur int, path []int) {
			for next := range hops[cur] {
				if next == g.Destination || next < start {
					continue // 保证 start 是环上最小 pivot
				}
				if next == start && len(path) >= 2 {
					cycles = append(cycles, append([]int(nil), path...))
					continue
				}
				if visited[next] {
					continue
				}
				visited[next] = true
				dfs(next, append(path, next))
				delete(visited, next)
			}
		}
		dfs(start, []int{start})
	}
	sort.Slice(cycles, func(i, j int) bool {
		if len(cycles[i]) != len(cycles[j]) {
			return len(cycles[i]) < len(cycles[j])
		}
		for k := range cycles[i] {
			if cycles[i][k] != cycles[j][k] {
				return cycles[i][k] < cycles[j][k]
			}
		}
		return false
	})
	return cycles
}

func pathHasOtherPivot(p Path, self int, pivots map[int]bool) bool {
	for _, as := range p {
		if as != self && pivots[as] {
			return true
		}
	}
	return false
}

type wheelCandidate struct {
	spoke   Path
	rim     Path
	spokeRk int
	rimRk   int
}

// bestWheelCandidate 在 pivot u 上寻找满足 R >_u Q 的最优 (Q, R) 组合。
func (g *Graph) bestWheelCandidate(u, rimNext int, pivotSet map[int]bool) (wheelCandidate, bool) {
	prefs := g.Preferences(u)
	var best wheelCandidate
	found := false
	for _, q := range prefs {
		if pathHasOtherPivot(q, u, pivotSet) {
			continue
		}
		qRank := g.Rank(u, q)
		for _, r := range g.PathsVia(u, rimNext) {
			rRank := g.Rank(u, r)
			if rRank >= qRank {
				continue // 必须严格更优
			}
			cand := wheelCandidate{spoke: copyPath(q), rim: copyPath(r), spokeRk: qRank, rimRk: rRank}
			if !found || cand.spokeRk < best.spokeRk ||
				(cand.spokeRk == best.spokeRk && cand.rimRk < best.rimRk) ||
				(cand.spokeRk == best.spokeRk && cand.rimRk == best.rimRk && pathLess(cand.rim, best.rim)) {
				best = cand
				found = true
			}
		}
	}
	return best, found
}

// FindDisputeWheels 在实例上构造争议轮，最多返回 limit 个（limit<=0 时
// 使用 DefaultWheelLimit）。结果按环长、pivot 字典序确定排序。
func (g *Graph) FindDisputeWheels(limit int) []*DisputeWheel {
	if limit <= 0 {
		limit = DefaultWheelLimit
	}
	hops := g.nextHopMap()
	cycles := canonicalCycles(g, hops)
	var wheels []*DisputeWheel

	for _, cycle := range cycles {
		pivotSet := make(map[int]bool, len(cycle))
		for _, u := range cycle {
			pivotSet[u] = true
		}
		k := len(cycle)
		wheel := &DisputeWheel{Pivots: append([]int(nil), cycle...)}
		ok := true
		for i, u := range cycle {
			rimNext := cycle[(i+1)%k]
			cand, valid := g.bestWheelCandidate(u, rimNext, pivotSet)
			if !valid {
				ok = false
				break
			}
			wheel.Spokes = append(wheel.Spokes, cand.spoke)
			wheel.Rims = append(wheel.Rims, cand.rim)
			wheel.SpokeRanks = append(wheel.SpokeRanks, cand.spokeRk)
			wheel.RimRanks = append(wheel.RimRanks, cand.rimRk)
		}
		if !ok {
			continue
		}
		for i, u := range cycle {
			wheel.Proof = append(wheel.Proof, fmt.Sprintf(
				"pivot AS%d: R_%d = [%s] (rank=%d) >_%d Q_%d = [%s] (rank=%d)",
				u, i, wheel.Rims[i], wheel.RimRanks[i], u, i, wheel.Spokes[i], wheel.SpokeRanks[i]))
		}
		wheels = append(wheels, wheel)
		if len(wheels) >= limit {
			break
		}
	}
	return wheels
}
