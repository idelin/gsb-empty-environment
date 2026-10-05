package main

import "container/heap"

// referenceSolve 是用于小图复算的参照实现：
// 把图展开成状态 (点, 已用风险)，在状态图上跑 Dijkstra。
// 每个状态只留 (费用, 边数, 路径字典序) 最优的一条，这在全序比较下是精确的。
// 零费用零风险环回到同一状态，不会更优，自然终止。

type refKey struct {
	node int
	risk int
}

type refEntry struct {
	key  refKey
	cost int64
	hops int
	path []int
}

func refLess(a, b *refEntry) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.hops != b.hops {
		return a.hops < b.hops
	}
	return comparePaths(a.path, b.path) < 0
}

type refHeap []*refEntry

func (h refHeap) Len() int            { return len(h) }
func (h refHeap) Less(i, j int) bool  { return refLess(h[i], h[j]) }
func (h refHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *refHeap) Push(x interface{}) { *h = append(*h, x.(*refEntry)) }
func (h *refHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func referenceSolve(g *Graph, src, dst, budget int) Solution {
	dist := make(map[refKey]*refEntry)
	visited := make(map[refKey]bool)
	pq := &refHeap{}
	start := &refEntry{key: refKey{node: src, risk: 0}}
	dist[start.key] = start
	heap.Push(pq, start)
	for pq.Len() > 0 {
		cur := heap.Pop(pq).(*refEntry)
		if visited[cur.key] {
			continue
		}
		visited[cur.key] = true
		for _, e := range g.Adj[cur.key.node] {
			nr := cur.key.risk + e.Risk
			if nr > budget {
				continue
			}
			nk := refKey{node: e.To, risk: nr}
			if visited[nk] {
				continue
			}
			path := make([]int, len(cur.path)+1)
			copy(path, cur.path)
			path[len(cur.path)] = e.ID
			cand := &refEntry{key: nk, cost: cur.cost + e.Cost, hops: cur.hops + 1, path: path}
			if old, ok := dist[nk]; !ok || refLess(cand, old) {
				dist[nk] = cand
				heap.Push(pq, cand)
			}
		}
	}
	best := Solution{}
	var bestEntry *refEntry
	for r := 0; r <= budget; r++ {
		e, ok := dist[refKey{node: dst, risk: r}]
		if !ok {
			continue
		}
		if bestEntry == nil || refLess(e, bestEntry) {
			bestEntry = e
			best = Solution{Found: true, Cost: e.cost, Risk: e.key.risk, Path: e.path}
		}
	}
	return best
}
