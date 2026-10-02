// Demo of the dynamic topological ordering engine: cycle rejection
// with exact causal path, and bidirectional local recoloring.
package main

import (
	"fmt"

	"dyntopo"
)

func main() {
	g := dyntopo.New[int]()
	for _, e := range [][2]int{{1, 2}, {2, 3}, {3, 4}} {
		g.AddEdge(e[0], e[1])
	}
	fmt.Println("initial deps 1->2->3->4, topo order:", g.TopoOrder())

	fmt.Println("\n--- add 4->2 (closes a deadlock cycle) ---")
	if cyc, rejected := g.AddEdge(4, 2); rejected {
		fmt.Println("REJECTED, causal cycle:", cyc)
	}
	fmt.Println("topo order unchanged:  ", g.TopoOrder())

	fmt.Println("\n--- add 1->4 (consistent with current order) ---")
	if _, rejected := g.AddEdge(1, 4); !rejected {
		fmt.Println("accepted, fast path, topo order:", g.TopoOrder())
	}

	fmt.Println("\n--- backward edge: bidirectional local recoloring ---")
	h := dyntopo.New[int]()
	for _, e := range [][2]int{{1, 2}, {3, 4}, {5, 6}} {
		h.AddEdge(e[0], e[1])
	}
	fmt.Println("before 4->2:", ranks(h, 1, 2, 3, 4, 5, 6))
	h.AddEdge(4, 2)
	fmt.Println("after  4->2:", ranks(h, 1, 2, 3, 4, 5, 6),
		"(only nodes 2,3,4 recolored; 1,5,6 untouched)")
}

func ranks(g *dyntopo.Graph[int], vs ...int) string {
	s := ""
	for _, v := range vs {
		o, _ := g.Order(v)
		s += fmt.Sprintf(" %d:%d", v, o)
	}
	return s
}
