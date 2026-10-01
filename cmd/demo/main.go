// Command demo runs the acceptance scenario end to end: build the diamond,
// add the bypass edge, remove one branch edge, and print a counterexample
// path for a non-domination query.
package main

import (
	"fmt"
	"strings"

	"dynidom/dominator"
)

func main() {
	g := dominator.New("S")
	for _, e := range [][2]string{{"S", "A"}, {"S", "B"}, {"A", "T"}, {"B", "T"}} {
		g.AddEdge(e[0], e[1])
	}
	fmt.Println("== diamond S->A, S->B, A->T, B->T ==")
	fmt.Print(g)

	g.AddEdge("S", "T")
	fmt.Println("== after adding bypass edge S->T ==")
	fmt.Print(g)

	g.RemoveEdge("S", "T") // restore the plain diamond
	g.RemoveEdge("B", "T")
	fmt.Println("== after removing edge B->T from the diamond ==")
	fmt.Print(g)

	// Counterexample on the original diamond: prove A does not dominate T.
	g2 := dominator.New("S")
	for _, e := range [][2]string{{"S", "A"}, {"S", "B"}, {"A", "T"}, {"B", "T"}} {
		g2.AddEdge(e[0], e[1])
	}
	path, err := g2.Counterexample("A", "T")
	if err != nil {
		fmt.Println("no counterexample:", err)
		return
	}
	fmt.Printf("counterexample: A does not dominate T, witness path: %s\n",
		strings.Join(path, " -> "))
}
