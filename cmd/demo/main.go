// Command demo walks through the acceptance scenario: a diamond CFG whose
// dominator tree is updated incrementally as edges change, plus a
// counterexample path for a dominance claim that does not hold.
package main

import (
	"fmt"
	"strings"

	"dom"
)

func newDiamond() *dom.Graph {
	g := dom.NewGraph()
	g.AddEdge("S", "A")
	g.AddEdge("S", "B")
	g.AddEdge("A", "T")
	g.AddEdge("B", "T")
	return g
}

func printIdoms(tr *dom.Tree, nodes ...string) {
	for _, n := range nodes {
		if d, ok := tr.Idom(n); ok {
			fmt.Printf("  idom(%s) = %s\n", n, d)
		} else {
			fmt.Printf("  idom(%s) = <none: entry or unreachable>\n", n)
		}
	}
}

func main() {
	tr := dom.Compute(newDiamond(), "S")
	fmt.Println("1) Diamond S -> {A, B} -> T:")
	printIdoms(tr, "A", "B", "T")

	fmt.Println("2) Add bypass edge S -> T (incremental update):")
	tr.AddEdge("S", "T")
	printIdoms(tr, "T")

	tr2 := dom.Compute(newDiamond(), "S")
	fmt.Println("3) Remove edge B -> T from the diamond (incremental update):")
	tr2.RemoveEdge("B", "T")
	printIdoms(tr2, "T")

	fmt.Println("4) Counterexample for the claim \"A dominates T\" in the diamond:")
	tr3 := dom.Compute(newDiamond(), "S")
	if path, ok := tr3.Counterexample("A", "T"); ok {
		fmt.Printf("  A does not dominate T, witness path: %s\n", strings.Join(path, " -> "))
	} else {
		fmt.Println("  no counterexample (A dominates T)")
	}
}
