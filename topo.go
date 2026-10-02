// Package topo implements a bidirectional incremental topological sort
// engine with immediate cycle detection for large dynamic dependency
// graphs.
//
// The reordering strategy is the Pearce-Kelly dynamic topological sort:
// each vertex carries a dense integer "color" (its topological index).
// Inserting edge x->y is O(1) when ord[x] < ord[y]. Otherwise two local
// searches run toward each other - a forward DFS from y bounded by
// ord[x], and a backward DFS from x bounded by ord[y] - and only the
// vertices inside that affected region are recolored, by swapping the
// two blocks' existing index values. If the forward search reaches x,
// the new edge closes a cycle: the insertion is rejected, the exact
// causal cycle path is reported, and no state is mutated.
//
// All mutations are serialized with a sync.RWMutex, so concurrent
// AddEdge calls and concurrent readers are safe.
package topo

import (
	"fmt"
	"sort"
	"sync"
)

// CycleError is returned by AddEdge when the insertion would close a
// dependency cycle. Path lists the cycle's nodes in causal order, with
// the first node repeated at the end, e.g. [2 3 4 2].
type CycleError struct {
	From, To int
	Path     []int
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("topo: edge %d->%d rejected, dependency cycle: %v", e.From, e.To, e.Path)
}

// Graph is a concurrency-safe dynamic DAG with a maintained topological
// ordering.
type Graph struct {
	mu    sync.RWMutex
	nodes map[int]struct{}
	out   map[int]map[int]struct{}
	in    map[int]map[int]struct{}
	ord   map[int]int
	next  int
}

// New returns an empty Graph.
func New() *Graph {
	return &Graph{
		nodes: make(map[int]struct{}),
		out:   make(map[int]map[int]struct{}),
		in:    make(map[int]map[int]struct{}),
		ord:   make(map[int]int),
	}
}

// AddNode registers id (no-op if present). New nodes are appended at the
// end of the current ordering.
func (g *Graph) AddNode(id int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addNodeLocked(id)
}

func (g *Graph) addNodeLocked(id int) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.ord[id] = g.next
	g.next++
}

// AddEdge inserts the dependency from->to. It returns a *CycleError -
// without mutating any state - if the edge would close a cycle.
// Otherwise it recolors only the affected region and returns nil.
func (g *Graph) AddEdge(from, to int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.addNodeLocked(from)
	g.addNodeLocked(to)

	if from == to {
		return &CycleError{From: from, To: to, Path: []int{from, to}}
	}
	if _, dup := g.out[from][to]; dup {
		return nil
	}

	// Fast path: the existing order already satisfies the new edge.
	if g.ord[from] < g.ord[to] {
		g.insertLocked(from, to)
		return nil
	}

	deltaF, deltaB, cycle := g.discover(from, to)
	if cycle != nil {
		return &CycleError{From: from, To: to, Path: cycle}
	}
	g.recolor(deltaF, deltaB)
	g.insertLocked(from, to)
	return nil
}

func (g *Graph) insertLocked(from, to int) {
	if g.out[from] == nil {
		g.out[from] = make(map[int]struct{})
	}
	g.out[from][to] = struct{}{}
	if g.in[to] == nil {
		g.in[to] = make(map[int]struct{})
	}
	g.in[to][from] = struct{}{}
}

// discover runs the bidirectional local search for a candidate edge
// from->to where ord[from] >= ord[to].
//
//	deltaF: vertices reachable forward from `to` with ord <= ord[from]
//	deltaB: vertices reaching `from` backward with ord >= ord[to]
//
// If the forward frontier reaches `from`, the edge closes a cycle; the
// exact cycle path is returned instead. The two sets are disjoint
// unless a cycle exists, so detecting the reachability of `from` is
// sufficient.
func (g *Graph) discover(from, to int) (deltaF, deltaB []int, cycle []int) {
	upper := g.ord[from]
	lower := g.ord[to]

	visitedF := map[int]bool{to: true}
	parent := map[int]int{to: -1}
	stack := []int{to}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range g.sortedOut(v) {
			if g.ord[w] > upper {
				continue
			}
			if w == from {
				parent[from] = v
				return nil, nil, buildCycle(parent, from, to)
			}
			if !visitedF[w] {
				visitedF[w] = true
				parent[w] = v
				stack = append(stack, w)
			}
		}
	}
	for v := range visitedF {
		deltaF = append(deltaF, v)
	}

	visitedB := map[int]bool{from: true}
	stack = []int{from}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for w := range g.in[v] {
			if g.ord[w] < lower || visitedB[w] {
				continue
			}
			visitedB[w] = true
			stack = append(stack, w)
		}
	}
	for v := range visitedB {
		deltaB = append(deltaB, v)
	}
	return deltaF, deltaB, nil
}

// buildCycle reconstructs the causal cycle to ~> from, closed by the
// rejected edge from->to, e.g. [2 3 4 2].
func buildCycle(parent map[int]int, from, to int) []int {
	rev := []int{from}
	for v := from; v != to; {
		v = parent[v]
		rev = append(rev, v)
	}
	cycle := make([]int, 0, len(rev)+1)
	for i := len(rev) - 1; i >= 0; i-- {
		cycle = append(cycle, rev[i])
	}
	return append(cycle, to)
}

// recolor performs the bidirectional local recoloring: the existing
// index values of deltaB and deltaF are pooled, sorted, and reassigned
// so that every deltaB vertex keeps a lower index than every deltaF
// vertex, while each block preserves its internal relative order. No
// new index values are allocated and untouched vertices keep their
// colors, so the perturbation is minimal.
func (g *Graph) recolor(deltaF, deltaB []int) {
	byOrd := func(vs []int) {
		sort.Slice(vs, func(i, j int) bool { return g.ord[vs[i]] < g.ord[vs[j]] })
	}
	byOrd(deltaF)
	byOrd(deltaB)

	ranks := make([]int, 0, len(deltaB)+len(deltaF))
	for _, v := range deltaB {
		ranks = append(ranks, g.ord[v])
	}
	for _, v := range deltaF {
		ranks = append(ranks, g.ord[v])
	}
	sort.Ints(ranks)

	for i, v := range deltaB {
		g.ord[v] = ranks[i]
	}
	for i, v := range deltaF {
		g.ord[v] = ranks[len(deltaB)+i]
	}
}

func (g *Graph) sortedOut(v int) []int {
	ws := make([]int, 0, len(g.out[v]))
	for w := range g.out[v] {
		ws = append(ws, w)
	}
	sort.Ints(ws)
	return ws
}

// TopologicalOrder returns all node IDs sorted by their current
// topological index.
func (g *Graph) TopologicalOrder() []int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := make([]int, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return g.ord[ids[i]] < g.ord[ids[j]] })
	return ids
}

// Rank returns the current topological index of id.
func (g *Graph) Rank(id int) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ord[id]
}

// Validate checks that every edge satisfies ord[from] < ord[to].
// Intended for tests and diagnostics.
func (g *Graph) Validate() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for from, ws := range g.out {
		for to := range ws {
			if g.ord[from] >= g.ord[to] {
				return false
			}
		}
	}
	return true
}
