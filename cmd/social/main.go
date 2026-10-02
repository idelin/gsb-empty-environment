// Command social demonstrates dynamic maximal clique tracking on a small
// social network: tight circles (maximal cliques) are discovered, a new
// friendship completes a 5-person fully connected circle, and a removed
// friendship degrades it again.
package main

import (
	"fmt"
	"strings"

	"dynclique"
)

var names = []string{"Alice", "Bob", "Carol", "Dave", "Erin", "Frank"}

func id(name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	panic("unknown name " + name)
}

func render(cliques [][]int) string {
	if len(cliques) == 0 {
		return "  (none)"
	}
	var lines []string
	for _, c := range cliques {
		var members []string
		for _, v := range c {
			members = append(members, names[v])
		}
		lines = append(lines, fmt.Sprintf("  {%s}  (size %d)", strings.Join(members, ", "), len(c)))
	}
	return strings.Join(lines, "\n")
}

func main() {
	g := dynclique.NewGraph()
	befriend := func(a, b string) {
		fmt.Printf("+ friendship %s -- %s\n", a, b)
		g.AddEdge(id(a), id(b))
	}
	unfriend := func(a, b string) {
		fmt.Printf("- friendship %s -- %s\n", a, b)
		g.RemoveEdge(id(a), id(b))
	}

	fmt.Println("== Building the network ==")
	// Alice, Carol, Dave, Erin form a tight circle; Bob knows Carol, Dave
	// and Erin but not Alice yet. Frank is friends with Alice and Bob.
	for _, pair := range [][2]string{
		{"Alice", "Carol"}, {"Alice", "Dave"}, {"Alice", "Erin"},
		{"Carol", "Dave"}, {"Carol", "Erin"}, {"Dave", "Erin"},
		{"Bob", "Carol"}, {"Bob", "Dave"}, {"Bob", "Erin"},
		{"Frank", "Alice"}, {"Frank", "Bob"},
	} {
		befriend(pair[0], pair[1])
	}

	fmt.Println("\n== Tight circles (maximal cliques) ==")
	fmt.Println(render(g.MaximalCliques()))

	fmt.Println("\n== Circles containing Carol & Dave ==")
	fmt.Println(render(g.CliquesContaining(id("Carol"), id("Dave"))))

	fmt.Println("\n== Bob meets Alice: one edge completes a 5-person circle ==")
	befriend("Alice", "Bob")
	fmt.Println("New maximal clique born; the two absorbed 4-cliques are gone:")
	fmt.Println(render(g.MaximalCliques()))

	fmt.Println("\n== Bob and Alice fall out: the circle degrades ==")
	unfriend("Alice", "Bob")
	fmt.Println(render(g.MaximalCliques()))

	fmt.Println("\n== Carol and Erin fall out too ==")
	unfriend("Carol", "Erin")
	fmt.Println(render(g.MaximalCliques()))
}
