// Package main 实现两组带时间区间记录的匹配器。
//
// 规则:
//   - 只有正长度重叠才能配对, 端点相接 (overlap == 0) 不算重叠;
//   - 每条记录最多参与一对匹配 (一对一);
//   - 目标按字典序优化: 先最大化匹配对数, 再最大化总收益 (收益 = 重叠长度)。
package main

import (
	"container/heap"
	"fmt"
	"sort"
)

// Record 是一条带 ID 的半开时间区间记录 [Start, End)。
type Record struct {
	ID    string
	Start int64
	End   int64
}

// Overlap 返回 a 与 b 的正重叠长度; 端点相接或不相交返回 0。
func Overlap(a, b Record) int64 {
	lo := a.Start
	if b.Start > lo {
		lo = b.Start
	}
	hi := a.End
	if b.End < hi {
		hi = b.End
	}
	if hi > lo {
		return hi - lo
	}
	return 0
}

// Candidate 是一条可行匹配边: A[AI] 与 B[BI] 有正重叠, 权重为重叠长度。
type Candidate struct {
	AI     int
	BI     int
	Weight int64
}

// endHeap 是带外部 End 数组比较的堆, 避免闭包陷阱。
type endHeap struct {
	idx []int
	end []int64
}

func (h endHeap) Len() int            { return len(h.idx) }
func (h endHeap) Less(i, j int) bool  { return h.end[h.idx[i]] < h.end[h.idx[j]] }
func (h endHeap) Swap(i, j int)       { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }
func (h *endHeap) Push(x interface{}) { h.idx = append(h.idx, x.(int)) }
func (h *endHeap) Pop() interface{} {
	old := h.idx
	n := len(old)
	v := old[n-1]
	h.idx = old[:n-1]
	return v
}

// BuildCandidates 利用区间结构 (扫描线) 生成候选边, 避免 O(nA*nB) 全量两两比较。
//
// 做法: A、B 分别按 Start 排序; 按 A 的 Start 扫描, 把所有 Start < A.End 的 B
// 加入活跃堆 (按 End 组织的最小堆), 同时弹出 End <= A.Start 的 B (它们与当前
// 及之后所有 A 都不可能正重叠)。堆中剩下的 B 与当前 A 必有正重叠。
func BuildCandidates(a, b []Record) []Candidate {
	ai := make([]int, len(a))
	for i := range ai {
		ai[i] = i
	}
	sort.Slice(ai, func(i, j int) bool { return a[ai[i]].Start < a[ai[j]].Start })

	bi := make([]int, len(b))
	for i := range bi {
		bi[i] = i
	}
	sort.Slice(bi, func(i, j int) bool { return b[bi[i]].Start < b[bi[j]].Start })

	bEnd := make([]int64, len(b))
	for i, r := range b {
		bEnd[i] = r.End
	}

	var cands []Candidate
	act := &endHeap{end: bEnd}
	p := 0 // 下一个待加入活跃集的 B (按 Start 有序)
	for _, i := range ai {
		for p < len(bi) && b[bi[p]].Start < a[i].End {
			heap.Push(act, bi[p])
			p++
		}
		for act.Len() > 0 && bEnd[act.idx[0]] <= a[i].Start {
			heap.Pop(act)
		}
		for _, j := range act.idx {
			if w := Overlap(a[i], b[j]); w > 0 {
				cands = append(cands, Candidate{AI: i, BI: j, Weight: w})
			}
		}
	}
	return cands
}

// Pair 是一对匹配结果。
type Pair struct {
	AID     string
	BID     string
	Overlap int64
}

// Evidence 解释一条记录为何未匹配。
type Evidence struct {
	ID      string
	Reason  string
	Details []string
}

// Result 是匹配输出: 匹配对 + 双方未匹配记录的证据。
type Result struct {
	Pairs      []Pair
	Count      int
	TotalGain  int64
	UnmatchedA []Evidence
	UnmatchedB []Evidence
}

// ---- 最小费用最大流 (SSP + 势差 Dijkstra), 费用为负的重叠长度 ----
// 源 -> 每个 A (容量 1, 费用 0)
// A -> B (容量 1, 费用 -overlap), 仅候选边
// 每个 B -> 汇 (容量 1, 费用 0)
// 最大流 = 最大匹配数; 在最大流中取最小费用 = 最大化总重叠。
// 因为流量优先于费用, 天然实现 "先对数后收益" 的字典序。

type mcmfEdge struct {
	to, rev   int
	cap, cost int64
}

type mcmf struct {
	g [][]mcmfEdge
}

func (m *mcmf) addEdge(u, v int, cap, cost int64) {
	m.g[u] = append(m.g[u], mcmfEdge{to: v, rev: len(m.g[v]), cap: cap, cost: cost})
	m.g[v] = append(m.g[v], mcmfEdge{to: u, rev: len(m.g[u]) - 1, cap: 0, cost: -cost})
}

const inf = int64(1) << 60

// minCostMaxFlow 返回 (最大流, 最小费用)。
func (m *mcmf) minCostMaxFlow(s, t int) (int64, int64) {
	n := len(m.g)
	// Bellman-Ford 求初始势 (存在负费用边)。
	pot := make([]int64, n)
	for i := range pot {
		pot[i] = inf
	}
	pot[s] = 0
	for iter := 0; iter < n-1; iter++ {
		updated := false
		for u := 0; u < n; u++ {
			if pot[u] == inf {
				continue
			}
			for _, e := range m.g[u] {
				if e.cap > 0 && pot[u]+e.cost < pot[e.to] {
					pot[e.to] = pot[u] + e.cost
					updated = true
				}
			}
		}
		if !updated {
			break
		}
	}

	var flow, cost int64
	for {
		dist := make([]int64, n)
		prevV := make([]int, n)
		prevE := make([]int, n)
		for i := range dist {
			dist[i] = inf
		}
		dist[s] = 0
		// 简单 Dijkstra (图小, 用 O(n^2) 选择)。
		used := make([]bool, n)
		for {
			u := -1
			for i := 0; i < n; i++ {
				if !used[i] && dist[i] < inf && (u == -1 || dist[i] < dist[u]) {
					u = i
				}
			}
			if u == -1 {
				break
			}
			used[u] = true
			for ei, e := range m.g[u] {
				if e.cap <= 0 {
					continue
				}
				nd := dist[u] + e.cost + pot[u] - pot[e.to]
				if nd < dist[e.to] {
					dist[e.to] = nd
					prevV[e.to] = u
					prevE[e.to] = ei
				}
			}
		}
		if dist[t] == inf {
			break
		}
		for i := 0; i < n; i++ {
			if dist[i] < inf {
				pot[i] += dist[i]
			}
		}
		// 沿最短路增广 1 单位 (所有容量为 1 的瓶颈)。
		d := inf
		for v := t; v != s; v = prevV[v] {
			if c := m.g[prevV[v]][prevE[v]].cap; c < d {
				d = c
			}
		}
		for v := t; v != s; v = prevV[v] {
			e := &m.g[prevV[v]][prevE[v]]
			e.cap -= d
			m.g[v][e.rev].cap += d
			cost += d * e.cost
		}
		flow += d
	}
	return flow, cost
}

// Match 执行字典序最优匹配: 先最大化对数, 再最大化总重叠收益。
func Match(a, b []Record) Result {
	cands := BuildCandidates(a, b)

	nA, nB := len(a), len(b)
	src := 0
	aBase := 1
	bBase := 1 + nA
	snk := 1 + nA + nB
	m := &mcmf{g: make([][]mcmfEdge, snk+1)}
	for i := 0; i < nA; i++ {
		m.addEdge(src, aBase+i, 1, 0)
	}
	for j := 0; j < nB; j++ {
		m.addEdge(bBase+j, snk, 1, 0)
	}
	// 记录每条 A->B 边在邻接表中的位置, 便于事后读流量。
	edgePos := make([][2]int, len(cands))
	for k, c := range cands {
		edgePos[k] = [2]int{aBase + c.AI, len(m.g[aBase+c.AI])}
		m.addEdge(aBase+c.AI, bBase+c.BI, 1, -c.Weight)
	}

	flow, cost := m.minCostMaxFlow(src, snk)

	res := Result{Count: int(flow), TotalGain: -cost}

	matchedA := make([]int, nA) // A[i] 匹配到的 B 下标, -1 表示未匹配
	matchedB := make([]int, nB)
	for i := range matchedA {
		matchedA[i] = -1
	}
	for j := range matchedB {
		matchedB[j] = -1
	}
	for k, c := range cands {
		u, ei := edgePos[k][0], edgePos[k][1]
		if m.g[u][ei].cap == 0 { // 容量被用光 => 该边上有 1 单位流
			res.Pairs = append(res.Pairs, Pair{AID: a[c.AI].ID, BID: b[c.BI].ID, Overlap: c.Weight})
			matchedA[c.AI] = c.BI
			matchedB[c.BI] = c.AI
		}
	}

	// 为未匹配记录生成证据。
	candsOfA := make([][]int, nA)
	candsOfB := make([][]int, nB)
	for k, c := range cands {
		candsOfA[c.AI] = append(candsOfA[c.AI], k)
		candsOfB[c.BI] = append(candsOfB[c.BI], k)
	}
	for i := 0; i < nA; i++ {
		if matchedA[i] != -1 {
			continue
		}
		ev := Evidence{ID: a[i].ID}
		if len(candsOfA[i]) == 0 {
			ev.Reason = "与任何 B 记录都没有正长度重叠"
		} else {
			ev.Reason = "所有正重叠的 B 候选在最优解中已匹配给其他 A"
			for _, k := range candsOfA[i] {
				c := cands[k]
				ev.Details = append(ev.Details, fmt.Sprintf("候选 B[%s] 重叠=%d, 已匹配给 A[%s] (重叠=%d)",
					b[c.BI].ID, c.Weight, a[matchedB[c.BI]].ID, Overlap(a[matchedB[c.BI]], b[c.BI])))
			}
		}
		res.UnmatchedA = append(res.UnmatchedA, ev)
	}
	for j := 0; j < nB; j++ {
		if matchedB[j] != -1 {
			continue
		}
		ev := Evidence{ID: b[j].ID}
		if len(candsOfB[j]) == 0 {
			ev.Reason = "与任何 A 记录都没有正长度重叠"
		} else {
			ev.Reason = "所有正重叠的 A 候选在最优解中已匹配给其他 B"
			for _, k := range candsOfB[j] {
				c := cands[k]
				ev.Details = append(ev.Details, fmt.Sprintf("候选 A[%s] 重叠=%d, 已匹配给 B[%s] (重叠=%d)",
					a[c.AI].ID, c.Weight, b[matchedA[c.AI]].ID, Overlap(a[c.AI], b[matchedA[c.AI]])))
			}
		}
		res.UnmatchedB = append(res.UnmatchedB, ev)
	}
	return res
}
