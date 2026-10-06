package main

import "sort"

// Pair 表示一个可达点对 (from, to)，路径长度 >= 1。
type Pair struct {
	From, To int
}

// Change 表示一次可达性变化：Added=true 表示新增可达，否则不再可达。
type Change struct {
	Pair  Pair
	Added bool
}

// ReachIndex 维护图上所有正长度路径的可达性索引。
// 自环与环可以使节点到自身可达；桥边删除后由环自我支撑的
// 伪可达性会被正确清除（删除时对受影响源点重算）。
type ReachIndex struct {
	g     *Graph
	reach map[int]map[int]bool // from -> to 集合（正长度路径）
}

func NewReachIndex(g *Graph) *ReachIndex {
	return &ReachIndex{g: g, reach: make(map[int]map[int]bool)}
}

// pred 返回当前索引中能到达 u 的源点集合（含 u 自身，若 u 在环上）。
func (idx *ReachIndex) pred(u int) []int {
	var out []int
	for x, set := range idx.reach {
		if set[u] {
			out = append(out, x)
		}
	}
	return out
}

func (idx *ReachIndex) addPair(u, v int) bool {
	if idx.reach[u] == nil {
		idx.reach[u] = make(map[int]bool)
	}
	if idx.reach[u][v] {
		return false
	}
	idx.reach[u][v] = true
	return true
}

// Add 添加一条边（可重复），返回本次操作引起的可达性变化。
func (idx *ReachIndex) Add(u, v int) []Change {
	if !idx.g.AddEdge(u, v) {
		return nil // 重复边：结构未变，可达性不变
	}
	// 新路径必经过新边 (u,v)：源点集合 {u} ∪ 可达 u 的点，
	// 终点集合 {v} ∪ v 可达的点。空前缀/空后缀分别对应 s=u、t=v。
	var changes []Change
	sources := append(idx.pred(u), u)
	targets := []int{v}
	for t := range idx.reach[v] {
		targets = append(targets, t)
	}
	for _, s := range sources {
		for _, t := range targets {
			if idx.addPair(s, t) {
				changes = append(changes, Change{Pair{s, t}, true})
			}
		}
	}
	return changes
}

// Remove 删除一条边（计数减一），返回本次操作引起的可达性变化。
func (idx *ReachIndex) Remove(u, v int) []Change {
	if !idx.g.RemoveEdge(u, v) {
		return nil // 边不存在或仍有副本：结构未变
	}
	// 可能失效的点对 (a,b)：其所有 a->b 路径都经过 (u,v)，
	// 因此 a 必能到达 u（或 a==u）。只对这些源点重算即可，
	// 环上其余源点的重算会自然清除伪可达性。
	sources := append(idx.pred(u), u)
	var changes []Change
	for _, s := range sources {
		old := idx.reach[s]
		fresh := idx.g.BFSReach(s)
		for t := range old {
			if !fresh[t] {
				changes = append(changes, Change{Pair{s, t}, false})
			}
		}
		for t := range fresh {
			if !old[t] {
				changes = append(changes, Change{Pair{s, t}, true})
			}
		}
		idx.reach[s] = fresh
	}
	return changes
}

// Pairs 返回当前索引中全部可达点对（排序后）。
func (idx *ReachIndex) Pairs() []Pair {
	var out []Pair
	for u, set := range idx.reach {
		for v := range set {
			out = append(out, Pair{u, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// OracleReach 用全图 BFS 从头计算正长度可达点对，作为核对基准。
func OracleReach(g *Graph) map[Pair]bool {
	out := make(map[Pair]bool)
	for _, s := range g.Nodes() {
		for t := range g.BFSReach(s) {
			out[Pair{s, t}] = true
		}
	}
	return out
}

// VerifyAgainst 将索引与 BFS 基准比对，返回不一致的点对（空切片表示一致）。
func (idx *ReachIndex) VerifyAgainst(g *Graph) []Pair {
	oracle := OracleReach(g)
	got := make(map[Pair]bool)
	for _, p := range idx.Pairs() {
		got[p] = true
	}
	var diff []Pair
	for p := range oracle {
		if !got[p] {
			diff = append(diff, p)
		}
	}
	for p := range got {
		if !oracle[p] {
			diff = append(diff, p)
		}
	}
	sort.Slice(diff, func(i, j int) bool {
		if diff[i].From != diff[j].From {
			return diff[i].From < diff[j].From
		}
		return diff[i].To < diff[j].To
	})
	return diff
}
