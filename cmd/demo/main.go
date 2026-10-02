// Demo: build a layered DAG, add a cross-layer edge, watch the bitmap
// index update downstream reachability, then run O(1) queries.
package main

import (
	"fmt"

	"dagindex"
)

func main() {
	ix := dagindex.New()

	// Two parallel chains: 0->1->2->3 and 4->5->6->7.
	for i := 0; i < 8; i++ {
		ix.AddNode()
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 3}, {4, 5}, {5, 6}, {6, 7}} {
		must(ix.AddEdge(e[0], e[1]))
	}

	fmt.Println("== before cross-layer edge ==")
	fmt.Printf("reachable(0,7)=%v reachable(1,6)=%v\n", ix.Reachable(0, 7), ix.Reachable(1, 6))
	printStats(ix)

	// Cross-layer edge 1->6: nodes 0 and 1 instantly inherit {6,7}
	// through word-parallel bitmap OR along the predecessor chain.
	must(ix.AddEdge(1, 6))

	fmt.Println("== after cross-layer edge 1->6 ==")
	fmt.Printf("reachable(0,7)=%v reachable(1,6)=%v reachable(0,6)=%v\n",
		ix.Reachable(0, 7), ix.Reachable(1, 6), ix.Reachable(0, 6))
	fmt.Printf("reachable(4,0)=%v reachable(7,1)=%v (no backward paths)\n",
		ix.Reachable(4, 0), ix.Reachable(7, 1))
	fmt.Printf("levels: node1=%d node6=%d node7=%d\n", ix.Level(1), ix.Level(6), ix.Level(7))
	printStats(ix)

	// Cycle attempt is rejected.
	if err := ix.AddEdge(7, 0); err != nil {
		fmt.Println("cycle rejected:", err)
	}
}

func printStats(ix *dagindex.Index) {
	s := ix.Stats()
	fmt.Printf("stats: nodes=%d edges=%d reachablePairs=%d storedWords=%d denseWords=%d (%.1f%% of dense)\n",
		s.Nodes, s.Edges, s.ReachablePairs, s.StoredWords, s.DenseWords,
		100*float64(s.StoredWords)/float64(s.DenseWords))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
