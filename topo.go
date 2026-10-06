package eventgraph

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// CycleError is returned by TopoSort and LamportClocks when the graph
// contains a causality cycle. Cycle holds the evidence path (last == first).
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return "eventgraph: causality cycle: " + strings.Join(e.Cycle, " -> ")
}

// idHeap is a min-heap of event IDs, giving deterministic tie-breaking.
type idHeap []string

func (h idHeap) Len() int            { return len(h) }
func (h idHeap) Less(i, j int) bool  { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x interface{}) { *h = append(*h, x.(string)) }
func (h *idHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// TopoSort returns a deterministic topological order of all events:
// Kahn's algorithm with lexicographically-smallest ID chosen first among
// ready nodes. The result depends only on graph structure, never on
// insertion order or wall-clock timestamps. If the graph is cyclic, it
// returns a *CycleError whose Cycle is concrete evidence.
func (g *Graph) TopoSort() ([]*Event, error) {
	indeg := make(map[string]int, len(g.events))
	for id := range g.events {
		indeg[id] = 0
	}
	for from, tos := range g.edges {
		if _, ok := g.events[from]; !ok {
			continue
		}
		for to := range tos {
			if _, ok := g.events[to]; ok {
				indeg[to]++
			}
		}
	}

	ready := &idHeap{}
	for id, d := range indeg {
		if d == 0 {
			heap.Push(ready, id)
		}
	}

	order := make([]*Event, 0, len(g.events))
	for ready.Len() > 0 {
		id := heap.Pop(ready).(string)
		order = append(order, g.events[id])
		nexts := make([]string, 0, len(g.edges[id]))
		for to := range g.edges[id] {
			if _, ok := indeg[to]; ok {
				nexts = append(nexts, to)
			}
		}
		sort.Strings(nexts)
		for _, to := range nexts {
			indeg[to]--
			if indeg[to] == 0 {
				heap.Push(ready, to)
			}
		}
	}

	if len(order) != len(g.events) {
		return nil, &CycleError{Cycle: g.findCycle(indeg)}
	}
	return order, nil
}

// findCycle extracts one concrete cycle among the nodes left with positive
// in-degree after Kahn's algorithm stalls. Deterministic via sorted walks.
func (g *Graph) findCycle(indeg map[string]int) []string {
	remaining := make([]string, 0)
	for id, d := range indeg {
		if d > 0 {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	inRemaining := make(map[string]bool, len(remaining))
	for _, id := range remaining {
		inRemaining[id] = true
	}

	// Walk successors inside the remaining set; every node there has an
	// incoming edge from the set, so the walk must repeat a node.
	start := remaining[0]
	seen := map[string]int{}
	var walk []string
	cur := start
	for {
		if idx, ok := seen[cur]; ok {
			cycle := append([]string{}, walk[idx:]...)
			return append(cycle, cur)
		}
		seen[cur] = len(walk)
		walk = append(walk, cur)
		nexts := make([]string, 0)
		for to := range g.edges[cur] {
			if inRemaining[to] {
				nexts = append(nexts, to)
			}
		}
		if len(nexts) == 0 {
			// Should not happen in a genuine residual cycle; fall back.
			return append(walk, walk[0])
		}
		sort.Strings(nexts)
		cur = nexts[0]
	}
}

// LamportClocks assigns each event its Lamport logical clock: one plus the
// maximum clock of its causal predecessors (equivalently, the longest
// happens-before chain ending at the event). Computed over the
// deterministic topological order, so results are reproducible. Wall-clock
// timestamps play no part.
func (g *Graph) LamportClocks() (map[string]uint64, error) {
	order, err := g.TopoSort()
	if err != nil {
		return nil, err
	}
	clocks := make(map[string]uint64, len(order))
	for _, e := range order {
		var max uint64
		for from := range g.revEdge[e.ID] {
			if c, ok := clocks[from]; ok && c > max {
				max = c
			}
		}
		clocks[e.ID] = max + 1
	}
	return clocks, nil
}

// HappensBefore reports whether a is a causal ancestor of b.
func (g *Graph) HappensBefore(a, b string) bool {
	return g.path(a, b) != nil
}

// Describe renders the deterministic order with logical clocks and display
// wall-clock timestamps, one event per line.
func (g *Graph) Describe() (string, error) {
	order, err := g.TopoSort()
	if err != nil {
		return "", err
	}
	clocks, err := g.LamportClocks()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, e := range order {
		fmt.Fprintf(&b, "%3d. clock=%-4d %-24s wall=%s\n",
			i+1, clocks[e.ID], e.String(),
			e.WallClock.UTC().Format("15:04:05.000000"))
	}
	return b.String(), nil
}
