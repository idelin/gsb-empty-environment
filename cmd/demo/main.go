// Demo of the bidirectional incremental topological sort engine.
package main

import (
	"fmt"

	"dyntopo"
)

func main() {
	g := topo.New()

	fmt.Println("== build initial dependencies 1->2->3->4 ==")
	for _, e := range [][2]int{{1, 2}, {2, 3}, {3, 4}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			fmt.Println("unexpected:", err)
		}
	}
	fmt.Println("topological order:", g.TopologicalOrder())

	fmt.Println()
	fmt.Println("== insert 4->2 (closes a deadlock cycle) ==")
	if err := g.AddEdge(4, 2); err != nil {
		fmt.Println("rejected:", err)
	}
	fmt.Println("order after rejection:", g.TopologicalOrder())

	fmt.Println()
	fmt.Println("== insert 1->4 (fast path, no recoloring) ==")
	if err := g.AddEdge(1, 4); err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Println("topological order:", g.TopologicalOrder())

	fmt.Println()
	fmt.Println("== insert 5->2 (bidirectional local recoloring) ==")
	g.AddNode(5)
	fmt.Println("before:", g.TopologicalOrder())
	if err := g.AddEdge(5, 2); err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Println("after: ", g.TopologicalOrder())
	fmt.Println("valid: ", g.Validate())
}
