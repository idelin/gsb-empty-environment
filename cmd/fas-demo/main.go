// Command fas-demo builds a directed graph with three overlapping cycles
// that all share one key edge, runs every FAS heuristic plus the hybrid
// solver, and verifies the remaining graph is strictly acyclic.
package main

import (
	"fmt"

	"fas"
)

func main() {
	// Three overlapping directed cycles, all sharing edge 0->1 (w=5):
	//   X: 0->1->2->0
	//   Y: 0->1->3->0
	//   Z: 0->1->2->4->3->0
	g := fas.New(5)
	g.Add(0, 1, 5) // key edge shared by X, Y and Z
	g.Add(1, 2, 6)
	g.Add(2, 0, 6)
	g.Add(1, 3, 6)
	g.Add(3, 0, 6)
	g.Add(2, 4, 6)
	g.Add(4, 3, 6)

	fmt.Println("Directed graph with overlapping cycles X, Y, Z:")
	for _, e := range g.Edges {
		fmt.Printf("  %d -> %d  (w=%.0f)\n", e.From, e.To, e.Weight)
	}
	fmt.Printf("input graph is a DAG: %v\n\n", fas.IsDAG(g))

	fmt.Println("Heuristic results:")
	for _, h := range []struct {
		name string
		fn   func(*fas.Graph) []fas.Edge
	}{
		{"greedy-shared", fas.GreedyShared},
		{"greedy-min-weight", fas.GreedyMinWeight},
		{"topological", fas.Topological},
	} {
		rem := h.fn(g)
		fmt.Printf("  %-18s cut %v  total weight %.0f\n", h.name+":", edges(rem), fas.TotalWeight(rem))
	}

	res := fas.Solve(g)
	fmt.Printf("\nHybrid choice: %s, total removed weight %.0f\n", res.Heuristic, res.TotalWeight)
	fmt.Printf("removed edge set: %v\n", edges(res.Removed))

	rest := fas.Subtract(g, res.Removed)
	fmt.Printf("remaining graph is a DAG: %v\n", fas.IsDAG(rest))
	fmt.Printf("topological order of remaining graph: %v\n", res.TopoOrder)
	fmt.Printf("topological order verified: %v\n", fas.VerifyOrder(rest, res.TopoOrder))
}

func edges(es []fas.Edge) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = fmt.Sprintf("%d->%d(w=%.0f)", e.From, e.To, e.Weight)
	}
	return out
}
