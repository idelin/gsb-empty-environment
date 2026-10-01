// Command demo walks through the acceptance scenario and prints the
// SCC delta reported after every graph mutation.
package main

import (
	"fmt"

	"example.com/incscc/scc"
)

func show(m *scc.Maintainer, op string, ch scc.Change) {
	fmt.Printf(">> %s\n", op)
	for _, mg := range ch.Merges {
		fmt.Printf("   merged %v -> component %q with nodes %v\n", mg.From, mg.To.ID, mg.To.Nodes)
	}
	for _, sp := range ch.Splits {
		fmt.Printf("   split component %q ->", sp.From)
		for _, c := range sp.To {
			fmt.Printf(" %q%v", c.ID, c.Nodes)
		}
		fmt.Println()
	}
	if ch.Empty() {
		fmt.Println("   (no SCC change)")
	}
	fmt.Print("   components:")
	for _, c := range m.Components() {
		fmt.Printf(" %q%v", c.ID, c.Nodes)
	}
	fmt.Println()
}

func main() {
	m := scc.New()
	show(m, "add edge A->B", m.AddEdge("A", "B"))
	show(m, "add edge B->C", m.AddEdge("B", "C"))
	show(m, "add edge C->A", m.AddEdge("C", "A"))
	show(m, "add vertex D", m.AddVertex("D"))
	show(m, "add edge C->D", m.AddEdge("C", "D"))
	show(m, "remove edge B->C", m.RemoveEdge("B", "C"))
}
