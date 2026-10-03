// Command kcore-demo builds a graph with a dense sub-network and a
// dangling chain, prints the layered 1-core..4-core subgraphs, then
// inserts a bridging edge and shows that the incremental local update
// is far cheaper than a global recomputation.
package main

import (
	"fmt"

	"kcore"
)

func buildGraph() *kcore.Graph {
	g := kcore.New()

	// Dense sub-network A: 5-clique (core number 4).
	clique := []int{1, 2, 3, 4, 5}
	for i, a := range clique {
		for _, b := range clique[i+1:] {
			g.AddEdge(a, b)
		}
	}

	// Cluster B: K4 missing the edge (12,13) -> every vertex has
	// core number 2 until the missing edge is bridged.
	for _, e := range [][2]int{{10, 11}, {10, 12}, {10, 13}, {11, 12}, {11, 13}} {
		g.AddEdge(e[0], e[1])
	}

	// Ring C: a plain cycle (core number 2).
	for _, e := range [][2]int{{20, 21}, {21, 22}, {22, 23}, {23, 20}} {
		g.AddEdge(e[0], e[1])
	}

	// Dangling chain hanging off the dense clique (core number 1).
	g.AddEdge(1, 30)
	g.AddEdge(30, 31)
	g.AddEdge(31, 32)

	return g
}

func printLayers(g *kcore.Graph) {
	layers := g.Layers()
	fmt.Println("Core-Number per vertex (layered):")
	for k := g.MaxCore(); k >= 0; k-- {
		if vs, ok := layers[k]; ok {
			fmt.Printf("  coreness %d: %v\n", k, vs)
		}
	}
	fmt.Println("Layered k-core subgraphs (vertex sets):")
	for k := 1; k <= 4; k++ {
		fmt.Printf("  %d-core: %v\n", k, g.KCoreNodes(k))
	}
}

func main() {
	g := buildGraph()
	fmt.Printf("Graph: %d vertices, %d edges\n\n", g.NodeCount(), g.EdgeCount())

	fmt.Println("== Initial decomposition ==")
	printLayers(g)

	fmt.Println("\n== Insert bridging edge (12,13): completes cluster B into K4 ==")
	fmt.Printf("coreness of cluster B before: [%d %d %d %d]\n",
		g.CoreNumber(10), g.CoreNumber(11), g.CoreNumber(12), g.CoreNumber(13))
	g.AddEdge(12, 13)
	localWork := g.LastUpdateWork()
	fmt.Printf("coreness of cluster B after:  [%d %d %d %d]\n",
		g.CoreNumber(10), g.CoreNumber(11), g.CoreNumber(12), g.CoreNumber(13))
	fmt.Printf("incremental update visited %d vertices\n", localWork)

	g.Decompose()
	globalWork := g.LastUpdateWork()
	fmt.Printf("global recomputation visited %d vertices (%.1fx more work)\n",
		globalWork, float64(globalWork)/float64(localWork))

	fmt.Println("\n== Layers after the bridge ==")
	printLayers(g)

	fmt.Println("\n== Remove the bridge again: local demotion ==")
	g.RemoveEdge(12, 13)
	fmt.Printf("coreness of cluster B: [%d %d %d %d], visited %d vertices\n",
		g.CoreNumber(10), g.CoreNumber(11), g.CoreNumber(12), g.CoreNumber(13),
		g.LastUpdateWork())
}
