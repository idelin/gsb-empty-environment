// Package dyntopo implements a bidirectional incremental topological
// ordering engine with exact cycle reporting.
//
// The engine maintains a valid topological ordering of a directed graph
// while edges are added dynamically. It uses the Pearce-Kelly (PK)
// algorithm: when a new edge from->to arrives with order[from] >
// order[to], only the nodes whose current order falls inside the
// affected interval [order[to], order[from]] are re-labeled
// ("recolored"), from both directions (forward from `to`, backward
// from `from`), which minimizes the number of touched nodes.
//
// If the new edge would close a cycle, the edge is rejected, the
// existing ordering is left untouched, and the full causal cycle
// (e.g. [2 3 4 2]) is returned.
//
// All methods are safe for concurrent use; edge insertion is
// serialized internally with a mutex.
package dyntopo

import (
	"sort"
	"sync"
)

// Graph is a dynamic directed graph maintaining a topological order.
type Graph[T comparable] struct {
	mu    sync.RWMutex
	succ  map[T]map[T]struct{}
	pred  map[T]map[T]struct{}
	order map[T]int
	next  int
}

// New returns an empty Graph.
func New[T comparable]() *Graph[T] {
	return &Graph[T]{
		succ:  make(map[T]map[T]struct{}),
		pred:  make(map[T]map[T]struct{}),
		order: make(map[T]int),
	}
}

// AddNode registers v (no-op if already present).
func (g *Graph[T]) AddNode(v T) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addNodeLocked(v)
}

func (g *Graph[T]) addNodeLocked(v T) {
	if _, ok := g.order[v]; ok {
		return
	}
	g.order[v] = g.next
	g.next++
	g.succ[v] = make(map[T]struct{})
	g.pred[v] = make(map[T]struct{})
}

// AddEdge attempts to add the dependency edge from->to.
//
// If the edge would create a cycle, AddEdge returns (cycle, true)
// where cycle is the full causal deadlock path with the first node
// repeated at the end (e.g. [2 3 4 2]); the graph and its ordering
// are left completely unmodified. Otherwise it returns (nil, false)
// and the ordering is adjusted, recoloring only the nodes inside the
// affected interval.
func (g *Graph[T]) AddEdge(from, to T) (cycle []T, rejected bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.addNodeLocked(from)
	g.addNodeLocked(to)

	if from == to {
		return []T{from, to}, true
	}
	if _, ok := g.succ[from][to]; ok {
		return nil, false // duplicate edge, nothing to do
	}

	if g.order[from] > g.order[to] {
		// The edge invalidates the current order. Check for a cycle:
		// a path to ~> from in the existing graph plus the new edge
		// from->to closes a loop.
		if path := g.findPathLocked(to, from, g.order[from]); path != nil {
			return append(path, to), true
		}
		// No cycle: bidirectional local recoloring (PK algorithm).
		g.rebalanceLocked(from, to)
	}

	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil, false
}

// findPathLocked returns a path src ~> dst visiting only nodes with
// order <= maxOrd, or nil if no such path exists. Neighbors are
// visited in ascending order so the reported cycle is deterministic.
func (g *Graph[T]) findPathLocked(src, dst T, maxOrd int) []T {
	if src == dst {
		return []T{src}
	}
	seen := map[T]bool{src: true}
	parent := make(map[T]T)
	stack := []T{src}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, m := range g.sortedSucc(n) {
			if seen[m] || g.order[m] > maxOrd {
				continue
			}
			seen[m] = true
			parent[m] = n
			if m == dst {
				rev := []T{dst}
				for cur := dst; cur != src; {
					cur = parent[cur]
					rev = append(rev, cur)
				}
				path := make([]T, len(rev))
				for i, v := range rev {
					path[len(rev)-1-i] = v
				}
				return path
			}
			stack = append(stack, m)
		}
	}
	return nil
}

// rebalanceLocked performs the PK bidirectional local recoloring for
// a new edge from->to where order[from] > order[to] and no cycle
// exists. Only nodes with order in [order[to], order[from]] that are
// reachable from `to` (forward) or can reach `from` (backward) are
// relabeled; every other node keeps its exact order value.
func (g *Graph[T]) rebalanceLocked(from, to T) {
	lb, ub := g.order[to], g.order[from]

	deltaF := g.collectLocked(to, ub, g.succ, func(o int) bool { return o <= ub })
	deltaB := g.collectLocked(from, lb, g.pred, func(o int) bool { return o >= lb })

	g.sortByOrder(deltaF)
	g.sortByOrder(deltaB)

	// Pool of positions to redistribute: exactly the old order values
	// of the affected nodes.
	pool := make([]int, 0, len(deltaF)+len(deltaB))
	for _, n := range deltaB {
		pool = append(pool, g.order[n])
	}
	for _, n := range deltaF {
		pool = append(pool, g.order[n])
	}
	sort.Ints(pool)

	// Backward region keeps the low positions, forward region the high
	// ones; each region preserves its internal relative order.
	layout := make([]T, 0, len(pool))
	layout = append(layout, deltaB...)
	layout = append(layout, deltaF...)
	for i, n := range layout {
		g.order[n] = pool[i]
	}
}

// collectLocked gathers all nodes reachable from src through edges,
// visiting only nodes whose order satisfies inRange.
func (g *Graph[T]) collectLocked(src T, _ int, edges map[T]map[T]struct{}, inRange func(int) bool) []T {
	seen := map[T]bool{src: true}
	out := []T{src}
	stack := []T{src}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for m := range edges[n] {
			if !seen[m] && inRange(g.order[m]) {
				seen[m] = true
				out = append(out, m)
				stack = append(stack, m)
			}
		}
	}
	return out
}

func (g *Graph[T]) sortedSucc(n T) []T {
	out := make([]T, 0, len(g.succ[n]))
	for m := range g.succ[n] {
		out = append(out, m)
	}
	g.sortByOrder(out)
	return out
}

func (g *Graph[T]) sortByOrder(nodes []T) {
	sort.Slice(nodes, func(i, j int) bool { return g.order[nodes[i]] < g.order[nodes[j]] })
}

// Order returns the current topological rank of v.
func (g *Graph[T]) Order(v T) (int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	o, ok := g.order[v]
	return o, ok
}

// HasEdge reports whether the edge from->to exists.
func (g *Graph[T]) HasEdge(from, to T) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.succ[from][to]
	return ok
}

// Size returns the number of nodes.
func (g *Graph[T]) Size() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.order)
}

// TopoOrder returns all nodes sorted by their current topological rank.
func (g *Graph[T]) TopoOrder() []T {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]T, 0, len(g.order))
	for v := range g.order {
		out = append(out, v)
	}
	g.sortByOrder(out)
	return out
}

// Edges returns all edges as [from, to] pairs, sorted by the current
// order of the source then the target.
func (g *Graph[T]) Edges() [][2]T {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([][2]T, 0)
	for u, ms := range g.succ {
		for v := range ms {
			out = append(out, [2]T{u, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := g.order[out[i][0]], g.order[out[j][0]]
		if oi != oj {
			return oi < oj
		}
		return g.order[out[i][1]] < g.order[out[j][1]]
	})
	return out
}
