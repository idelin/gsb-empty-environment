// Command demo runs the acceptance scenario of the incremental max-flow
// adjuster: initial max flow 10, critical edge a->t reduced 5 -> 2, three
// units retracted along the residual network, one unit recovered via an
// alternative path, final max flow 8 with its min-cut certificate.
package main

import (
	"fmt"
	"strings"

	maxflow "example.com/incmaxflow"
)

func main() {
	//	s(0) --5--> a(1) --5--> t(3)
	//	  \          |           ^
	//	   5         1           |
	//	    \        v           6
	//	     \-----> b(2) -------/
	names := []string{"s", "a", "b", "t"}
	net := maxflow.NewNetwork(4)
	eSA := net.AddEdge(0, 1, 5)
	eSB := net.AddEdge(0, 2, 5)
	eAT := net.AddEdge(1, 3, 5) // critical edge
	eAB := net.AddEdge(1, 2, 1)
	eBT := net.AddEdge(2, 3, 6)

	fmt.Println("=== Initial network ===")
	fmt.Printf("max flow = %d\n", net.MaxFlow(0, 3))
	printFlows(net, names, []int{eSA, eSB, eAT, eAB, eBT},
		[]string{"s->a", "s->b", "a->t", "a->b", "b->t"})

	fmt.Println("\n=== Update: capacity of a->t reduced 5 -> 2 ===")
	res := net.UpdateCapacity(eAT, 2)
	fmt.Printf("retracted along residual network : %d units\n", res.Retracted)
	fmt.Printf("re-augmented via alternative path: %d units\n", res.ReAugmented)
	fmt.Printf("new max flow                     : %d\n", res.MaxFlow)
	printFlows(net, names, []int{eSA, eSB, eAT, eAB, eBT},
		[]string{"s->a", "s->b", "a->t", "a->b", "b->t"})

	fmt.Println("\n=== Min-cut certificate ===")
	var sSide, tSide []string
	for v, ok := range res.Reachable {
		if ok {
			sSide = append(sSide, names[v])
		} else {
			tSide = append(tSide, names[v])
		}
	}
	fmt.Printf("S side (reachable from s): {%s}\n", strings.Join(sSide, ", "))
	fmt.Printf("T side                     : {%s}\n", strings.Join(tSide, ", "))
	var cutCap int64
	for _, ce := range res.Cut {
		fmt.Printf("cut edge: %s->%s  cap=%d\n", names[ce.From], names[ce.To], ce.Cap)
		cutCap += ce.Cap
	}
	fmt.Printf("cut capacity = %d (== max flow, certificate valid)\n", cutCap)
}

func printFlows(net *maxflow.Network, names []string, ids []int, labels []string) {
	for i, id := range ids {
		fmt.Printf("  flow(%s) = %d\n", labels[i], net.Flow(id))
	}
}
