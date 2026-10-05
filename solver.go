// Package rcsp solves the risk-constrained shortest path problem:
// given a directed graph with non-negative edge costs and integer edge
// risks, find the minimum-cost path from a source to a target whose
// total risk does not exceed a budget B.
//
// Objective ordering (lexicographic):
//  1. lower total cost
//  2. fewer hops (edges)
//  3. lexicographically smaller edge-ID sequence
//
// The solver keeps a Pareto frontier of non-dominated labels per node
// (it never collapses a node to a single best distance), so zero-cost
// cycles are handled and expensive-but-low-risk intermediate paths are
// preserved.
package rcsp

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
)

// Edge is a directed edge with a non-negative cost and a non-negative
// integer risk. IDs must be unique across the graph; they define the
// final tie-break ordering.
type Edge struct {
	ID   int
	From int
	To   int
	Cost int
	Risk int
}

// Graph is a directed graph stored as adjacency lists sorted by edge ID.
type Graph struct {
	NumNodes int
	Adj      [][]Edge
	byID     map[int]Edge
}

// NewGraph builds a Graph, validating the input.
func NewGraph(numNodes int, edges []Edge) (*Graph, error) {
	if numNodes <= 0 {
		return nil, fmt.Errorf("num_nodes must be positive, got %d", numNodes)
	}
	g := &Graph{NumNodes: numNodes, Adj: make([][]Edge, numNodes), byID: make(map[int]Edge, len(edges))}
	for _, e := range edges {
		if _, dup := g.byID[e.ID]; dup {
			return nil, fmt.Errorf("duplicate edge id %d", e.ID)
		}
		if e.From < 0 || e.From >= numNodes || e.To < 0 || e.To >= numNodes {
			return nil, fmt.Errorf("edge %d: endpoint out of range [%d,%d)", e.ID, 0, numNodes)
		}
		if e.Cost < 0 {
			return nil, fmt.Errorf("edge %d: negative cost %d", e.ID, e.Cost)
		}
		if e.Risk < 0 {
			return nil, fmt.Errorf("edge %d: negative risk %d (risks must be non-negative integers)", e.ID, e.Risk)
		}
		g.byID[e.ID] = e
		g.Adj[e.From] = append(g.Adj[e.From], e)
	}
	for i := range g.Adj {
		sort.Slice(g.Adj[i], func(a, b int) bool { return g.Adj[i][a].ID < g.Adj[i][b].ID })
	}
	return g, nil
}

// Edge returns the edge with the given ID.
func (g *Graph) Edge(id int) (Edge, bool) {
	e, ok := g.byID[id]
	return e, ok
}

// LedgerEntry is one line of the risk/cost ledger for a solution path.
type LedgerEntry struct {
	EdgeID  int
	From    int
	To      int
	Cost    int
	Risk    int
	CumCost int
	CumRisk int
}

// Result is the outcome of a solve. If Found is false no feasible path
// exists and the remaining fields are zero.
type Result struct {
	Found     bool
	EdgeIDs   []int
	Nodes     []int
	Hops      int
	TotalCost int
	TotalRisk int
	Ledger    []LedgerEntry
}

// label is a Pareto label: one non-dominated partial path ending at node.
type label struct {
	node   int
	cost   int
	risk   int
	hops   int
	seq    []int // edge IDs, in order
	pruned bool  // dominated after being pushed; skip on pop
}

// compareSeq compares two edge-ID sequences lexicographically.
func compareSeq(a, b []int) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// less orders labels by the objective: cost, then hops, then edge-ID seq.
func (l *label) less(o *label) bool {
	if l.cost != o.cost {
		return l.cost < o.cost
	}
	if l.hops != o.hops {
		return l.hops < o.hops
	}
	return compareSeq(l.seq, o.seq) < 0
}

// dominates reports whether l makes o useless. Risk is only a
// feasibility constraint, not an objective, so l must (a) use no more
// risk than o, and (b) be no worse under the objective ordering
// (cost, hops, edge-ID sequence). Then any continuation of o stays
// feasible for l and never scores better, so o can be discarded.
func (l *label) dominates(o *label) bool {
	if l.risk > o.risk {
		return false
	}
	if l.cost != o.cost {
		return l.cost < o.cost
	}
	if l.hops != o.hops {
		return l.hops < o.hops
	}
	return compareSeq(l.seq, o.seq) <= 0
}

// labelHeap is a priority queue ordered by the objective.
type labelHeap []*label

func (h labelHeap) Len() int { return len(h) }
func (h labelHeap) Less(i, j int) bool {
	return h[i].less(h[j])
}
func (h labelHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *labelHeap) Push(x interface{}) {
	*h = append(*h, x.(*label))
}
func (h *labelHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

var errBadEndpoint = errors.New("rcsp: endpoint out of range")

// Solve finds the minimum-cost path from source to target with total
// risk <= budget, breaking ties by hop count and then edge-ID sequence.
func Solve(g *Graph, source, target, budget int) (Result, error) {
	if source < 0 || source >= g.NumNodes || target < 0 || target >= g.NumNodes {
		return Result{}, errBadEndpoint
	}
	if budget < 0 {
		return Result{}, fmt.Errorf("negative budget %d", budget)
	}

	// kept[v] holds the current Pareto frontier of labels at v.
	kept := make([][]*label, g.NumNodes)
	pq := &labelHeap{}
	heap.Init(pq)

	start := &label{node: source}
	kept[source] = append(kept[source], start)
	heap.Push(pq, start)

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(*label)
		if cur.pruned {
			continue
		}
		if cur.node == target {
			return buildResult(g, cur), nil
		}
		for _, e := range g.Adj[cur.node] {
			nr := cur.risk + e.Risk
			if nr > budget {
				continue
			}
			seq := make([]int, len(cur.seq)+1)
			copy(seq, cur.seq)
			seq[len(cur.seq)] = e.ID
			nl := &label{node: e.To, cost: cur.cost + e.Cost, risk: nr, hops: cur.hops + 1, seq: seq}

			frontier := kept[e.To]
			dominated := false
			for _, l := range frontier {
				if !l.pruned && l.dominates(nl) {
					dominated = true
					break
				}
			}
			if dominated {
				continue
			}
			// Evict frontier labels that nl dominates.
			out := frontier[:0]
			for _, l := range frontier {
				if nl.dominates(l) {
					l.pruned = true
				} else {
					out = append(out, l)
				}
			}
			kept[e.To] = append(out, nl)
			heap.Push(pq, nl)
		}
	}
	return Result{Found: false}, nil
}

// buildResult materialises the path, totals and ledger of a label.
func buildResult(g *Graph, l *label) Result {
	res := Result{Found: true, Hops: l.hops, TotalCost: l.cost, TotalRisk: l.risk}
	res.EdgeIDs = append(res.EdgeIDs, l.seq...)
	if len(l.seq) > 0 {
		first, _ := g.Edge(l.seq[0])
		res.Nodes = append(res.Nodes, first.From)
	}
	cumCost, cumRisk := 0, 0
	for _, id := range l.seq {
		e, _ := g.Edge(id)
		cumCost += e.Cost
		cumRisk += e.Risk
		res.Nodes = append(res.Nodes, e.To)
		res.Ledger = append(res.Ledger, LedgerEntry{
			EdgeID:  e.ID,
			From:    e.From,
			To:      e.To,
			Cost:    e.Cost,
			Risk:    e.Risk,
			CumCost: cumCost,
			CumRisk: cumRisk,
		})
	}
	return res
}
