// Command fasdemo demonstrates the FAS approximator on a system with
// multiple overlapping directed cycles.
package main

import (
	"fmt"

	"fas"
)

func main() {
	// Nodes: 0=A 1=B 2=C 3=D 4=E 5=F
	// Overlapping cycles:
	//   cycle 1: A->B->C->A
	//   cycle 2: B->C->D->B   (shares edge B->C with cycle 1)
	//   cycle 3: D->E->F->D
	names := []string{"A", "B", "C", "D", "E", "F"}
	g := fas.New(6,
		fas.Edge{From: 0, To: 1, Weight: 2}, // A->B
		fas.Edge{From: 1, To: 2, Weight: 5}, // B->C  shared key edge, highest weight
		fas.Edge{From: 2, To: 0, Weight: 1}, // C->A
		fas.Edge{From: 2, To: 3, Weight: 1}, // C->D
		fas.Edge{From: 3, To: 1, Weight: 1}, // D->B
		fas.Edge{From: 3, To: 4, Weight: 1}, // D->E
		fas.Edge{From: 4, To: 5, Weight: 1}, // E->F
		fas.Edge{From: 5, To: 3, Weight: 1}, // F->D
	)

	fmt.Println("input graph: 6 nodes, 8 edges, 3 overlapping directed cycles")
	fmt.Println()

	for _, res := range []*fas.Result{fas.GreedyCycleCut(g), fas.Hybrid(g)} {
		fmt.Printf("algorithm: %s\n", res.Algorithm)
		fmt.Printf("  cut set (weight %.0f):\n", res.RemovedWeight())
		for _, e := range res.Removed {
			fmt.Printf("    cut %s->%s (w=%.0f)\n", names[e.From], names[e.To], e.Weight)
		}
		fmt.Print("  topological order of remaining graph:")
		for _, v := range res.Order {
			fmt.Printf(" %s", names[v])
		}
		fmt.Println()
		fmt.Printf("  remaining graph is a DAG: %v\n", fas.IsDAG(g.NumNodes, res.Remaining))
		fmt.Printf("  topological order is valid: %v\n",
			fas.ValidateTopologicalOrder(g.NumNodes, res.Remaining, res.Order))
		fmt.Println()
	}
}
