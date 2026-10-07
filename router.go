// Package router finds minimum-total-time routes on a road network whose
// edges carry travel times, where some consecutive edge pairs are forbidden
// (turn restrictions) and others impose a turn waiting time.
//
// The search state is the *arrival edge*, not the vertex: two routes that
// reach the same vertex via different edges are different states, because
// the remaining cost depends on which edge you arrived on (turn
// restrictions and turn waits). Among all minimum-cost routes the one whose
// edge-id sequence is lexicographically smallest wins. Routes that repeat a
// state are never considered: repeating the arrival-edge state closes a
// cycle, zero-cost cycles are forbidden, and a positive-cost cycle can only
// increase the total, so an optimal route is always state-simple.
package router

import (
	"container/heap"
	"fmt"
)

// Edge is a directed road segment. IDs must be unique within a Graph.
type Edge struct {
	ID   int
	From int
	To   int
	Time int64
}

// Graph is a road network with turn restrictions and turn waiting times.
type Graph struct {
	edges     []Edge
	byID      map[int]int   // edge ID -> index in edges
	out       map[int][]int // vertex -> indices of outgoing edges
	forbidden map[[2]int]bool
	wait      map[[2]int]int64
}

// New builds a Graph from the given edges. Edge IDs must be unique and
// travel times must be non-negative.
func New(edges []Edge) (*Graph, error) {
	g := &Graph{
		edges:     append([]Edge(nil), edges...),
		byID:      make(map[int]int, len(edges)),
		out:       make(map[int][]int),
		forbidden: make(map[[2]int]bool),
		wait:      make(map[[2]int]int64),
	}
	for i, e := range edges {
		if e.Time < 0 {
			return nil, fmt.Errorf("router: edge %d has negative travel time %d", e.ID, e.Time)
		}
		if _, dup := g.byID[e.ID]; dup {
			return nil, fmt.Errorf("router: duplicate edge id %d", e.ID)
		}
		g.byID[e.ID] = i
		g.out[e.From] = append(g.out[e.From], i)
	}
	return g, nil
}

// ForbidTurn forbids traversing edge next immediately after edge prev.
func (g *Graph) ForbidTurn(prev, next int) error {
	if err := g.checkEdgePair(prev, next); err != nil {
		return err
	}
	g.forbidden[[2]int{prev, next}] = true
	return nil
}

// SetTurnWait sets the waiting time paid when entering edge next directly
// after edge prev. The wait is charged in addition to the edge travel
// times. No wait is charged before the first edge of a route.
func (g *Graph) SetTurnWait(prev, next int, wait int64) error {
	if err := g.checkEdgePair(prev, next); err != nil {
		return err
	}
	if wait < 0 {
		return fmt.Errorf("router: negative turn wait %d", wait)
	}
	g.wait[[2]int{prev, next}] = wait
	return nil
}

func (g *Graph) checkEdgePair(prev, next int) error {
	if _, ok := g.byID[prev]; !ok {
		return fmt.Errorf("router: unknown edge id %d", prev)
	}
	if _, ok := g.byID[next]; !ok {
		return fmt.Errorf("router: unknown edge id %d", next)
	}
	return nil
}

// turnCost reports whether the turn prev->next is allowed and its wait.
func (g *Graph) turnCost(prev, next int) (int64, bool) {
	pair := [2]int{prev, next}
	if g.forbidden[pair] {
		return 0, false
	}
	return g.wait[pair], true
}

// label is the best known route to one arrival-edge state.
type label struct {
	cost int64
	path []int // edge IDs, in traversal order
	ok   bool
}

// better reports whether (cost, path) beats the existing label.
func better(cost int64, path []int, l label) bool {
	if !l.ok || cost < l.cost {
		return true
	}
	return cost == l.cost && lessPath(path, l.path)
}

// lessPath compares edge-id sequences lexicographically; a proper prefix
// sorts first.
func lessPath(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func equalPath(a, b []int) bool {
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

func containsID(path []int, id int) bool {
	for _, x := range path {
		if x == id {
			return true
		}
	}
	return false
}

// queueItem is one queued state expansion.
type queueItem struct {
	edge int   // index of the arrival edge (the state)
	cost int64 // total cost of path
	path []int // edge IDs leading to the state, including edges[edge]
}

// priorityQueue orders by (cost, path) lexicographically.
type priorityQueue []queueItem

func (q priorityQueue) Len() int { return len(q) }
func (q priorityQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return lessPath(q[i].path, q[j].path)
}
func (q priorityQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *priorityQueue) Push(x interface{}) { *q = append(*q, x.(queueItem)) }
func (q *priorityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

// ShortestPath returns the minimum-total-time route from vertex src to
// vertex dst as a sequence of edge IDs, together with its total cost
// (edge travel times plus turn waits). Among equal-cost routes the
// lexicographically smallest edge-id sequence is returned. ok is false if
// dst is unreachable. If src == dst the route is empty with cost 0.
func (g *Graph) ShortestPath(src, dst int) (path []int, cost int64, ok bool) {
	if src == dst {
		return nil, 0, true
	}

	best := make([]label, len(g.edges)) // per arrival-edge state
	pq := &priorityQueue{}
	heap.Init(pq)

	push := func(edgeIdx int, c int64, p []int) {
		if !better(c, p, best[edgeIdx]) {
			return
		}
		best[edgeIdx] = label{cost: c, path: p, ok: true}
		heap.Push(pq, queueItem{edge: edgeIdx, cost: c, path: p})
	}

	for _, idx := range g.out[src] {
		e := g.edges[idx]
		push(idx, e.Time, []int{e.ID})
	}

	var result label
	for pq.Len() > 0 {
		item := heap.Pop(pq).(queueItem)
		cur := best[item.edge]
		// Skip stale entries superseded by a better label for this state.
		if item.cost != cur.cost || !equalPath(item.path, cur.path) {
			continue
		}
		e := g.edges[item.edge]
		if e.To == dst {
			// Record but do not expand: any continuation only adds
			// non-negative cost and makes the id sequence lexicographically
			// larger, so a route ends at its first arrival at dst.
			if better(item.cost, item.path, result) {
				result = label{cost: item.cost, path: item.path, ok: true}
			}
			continue
		}
		for _, nextIdx := range g.out[e.To] {
			ne := g.edges[nextIdx]
			wait, allowed := g.turnCost(e.ID, ne.ID)
			if !allowed {
				continue
			}
			// Never repeat a state: a repeated arrival edge closes a
			// cycle, which is either zero-cost (forbidden) or
			// positive-cost (never optimal).
			if containsID(item.path, ne.ID) {
				continue
			}
			np := make([]int, len(item.path)+1)
			copy(np, item.path)
			np[len(item.path)] = ne.ID
			push(nextIdx, item.cost+wait+ne.Time, np)
		}
	}

	if !result.ok {
		return nil, 0, false
	}
	return result.path, result.cost, true
}
