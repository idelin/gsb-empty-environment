// Package topo provides a deterministic topological sort with cycle
// reporting, plus an incremental execution planner built on top of it.
package topo

import "container/heap"

// Edge is a precedence constraint: From must be ordered before To.
type Edge struct {
	From, To int
}

// Result is the outcome of Sort.
type Result struct {
	// Order is the maximal executable prefix in deterministic order:
	// among all currently-executable tasks the one with the smallest
	// original position is always picked first.
	Order []int
	// Cycle is one real closed loop (Cycle[0] == Cycle[len(Cycle)-1])
	// among the tasks that could not be ordered. Empty when every task
	// was ordered. Tasks that are merely blocked by the cycle but are
	// not part of it are never reported here.
	Cycle []int
}

// Sort topologically sorts tasks 0..n-1 under the given constraints.
// Duplicate constraints are deduplicated. The result is deterministic:
// it is the lexicographically smallest topological order (by original
// position) of the executable prefix.
func Sort(n int, edges []Edge) Result {
	adj, indeg := buildGraph(n, edges)

	h := &intHeap{}
	heap.Init(h)
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			heap.Push(h, i)
		}
	}

	var order []int
	for h.Len() > 0 {
		u := heap.Pop(h).(int)
		order = append(order, u)
		for _, v := range adj[u] {
			indeg[v]--
			if indeg[v] == 0 {
				heap.Push(h, v)
			}
		}
	}
	if len(order) == n {
		return Result{Order: order}
	}

	blocked := make([]bool, n)
	for u := 0; u < n; u++ {
		blocked[u] = indeg[u] > 0
	}
	return Result{Order: order, Cycle: findCycle(adj, blocked)}
}

// buildGraph deduplicates edges and returns adjacency list + indegrees.
func buildGraph(n int, edges []Edge) ([][]int, []int) {
	adj := make([][]int, n)
	indeg := make([]int, n)
	seen := make(map[Edge]struct{}, len(edges))
	for _, e := range edges {
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}
	return adj, indeg
}

// findCycle returns one real cycle within the blocked subgraph using
// iterative DFS with three colors. Only edges between blocked nodes are
// followed, so the reported loop consists solely of truly cyclic tasks.
func findCycle(adj [][]int, blocked []bool) []int {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // finished
	)
	color := make([]int, len(adj))
	var stack []int

	var dfs func(u int) []int
	dfs = func(u int) []int {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range adj[u] {
			if !blocked[v] {
				continue
			}
			switch color[v] {
			case gray:
				// Back edge: extract the loop v ... u -> v.
				start := 0
				for stack[start] != v {
					start++
				}
				cycle := make([]int, 0, len(stack)-start+1)
				cycle = append(cycle, stack[start:]...)
				cycle = append(cycle, v)
				return cycle
			case white:
				if c := dfs(v); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}

	for u := range adj {
		if blocked[u] && color[u] == white {
			if c := dfs(u); c != nil {
				return c
			}
		}
	}
	return nil
}

type intHeap []int

func (h intHeap) Len() int            { return len(h) }
func (h intHeap) Less(i, j int) bool  { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x interface{}) { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
