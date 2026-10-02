// Command kcore-demo demonstrates K-Core decomposition, layered
// peeling, and incremental local core maintenance on a graph that
// mixes a dense subnet with pendant chains.
package main

import (
	"fmt"

	"kcore"
)

func buildGraph() *kcore.Graph {
	g := kcore.NewGraph()
	// Dense subnet: clique K5 on vertices 0..4 (core number 4).
	for i := 0; i < 5; i++ {
		for j := i + 1; j < 5; j++ {
			g.AddEdge(i, j)
		}
	}
	// Triangular prism on 10..15 with one rung (12,15) missing:
	// every vertex has degree >= 2, so the whole prism sits in the
	// 2-core but not in the 3-core.
	for _, e := range [][2]int{
		{10, 11}, {11, 12}, {10, 12},
		{13, 14}, {14, 15}, {13, 15},
		{10, 13}, {11, 14},
	} {
		g.AddEdge(e[0], e[1])
	}
	// Pendant chain hanging off the clique (core number 1).
	g.AddEdge(0, 20)
	g.AddEdge(20, 21)
	g.AddEdge(21, 22)
	// Isolated vertex (core number 0).
	g.AddVertex(30)
	return g
}

func printLayers(cores map[int]int) {
	layers := kcore.Layers(cores)
	for k := 0; k <= kcore.MaxCore(cores); k++ {
		fmt.Printf("  core=%d (shell): %v\n", k, layers[k])
	}
}

func printKCores(g *kcore.Graph, maxK int) {
	for k := 1; k <= maxK; k++ {
		sub := kcore.KCoreSubgraph(g, k)
		fmt.Printf("  %d-core: %d vertices, %d edges -> %v\n",
			k, sub.NumVertices(), sub.NumEdges(), sub.Vertices())
	}
}

func main() {
	g := buildGraph()
	fmt.Printf("graph: %d vertices, %d edges\n\n", g.NumVertices(), g.NumEdges())

	fmt.Println("== layered core numbers ==")
	cores := kcore.CoreNumbers(g)
	printLayers(cores)

	fmt.Println("\n== k-core subgraphs by peeling (degree < k removed until convergence) ==")
	printKCores(g, 4)

	m := kcore.NewMaintainer(g)
	fmt.Println("\n== insert bridge edge (12,15): completes the prism ==")
	m.InsertEdge(12, 15)
	fmt.Printf("  local update: visited %d vertices, updated %d (global recompute would touch %d)\n",
		m.Visited, m.Updated, g.NumVertices())
	fmt.Printf("  prism cores now: ")
	for v := 10; v <= 15; v++ {
		fmt.Printf("%d:%d ", v, m.Core(v))
	}
	fmt.Println()
	printLayers(m.Cores())

	fmt.Println("\n== remove bridge edge (12,15): prism falls back ==")
	m.RemoveEdge(12, 15)
	fmt.Printf("  local update: visited %d vertices, updated %d\n", m.Visited, m.Updated)
	fmt.Printf("  prism cores now: ")
	for v := 10; v <= 15; v++ {
		fmt.Printf("%d:%d ", v, m.Core(v))
	}
	fmt.Println()
}
