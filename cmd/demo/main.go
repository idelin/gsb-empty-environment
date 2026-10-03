// Command demo builds a layered DAG, queries reachability, inserts a
// cross-layer edge to trigger bitmap propagation and level relabeling,
// and reports the memory footprint of the dense vs. compressed index.
package main

import (
	"fmt"
	"math/rand"

	"dagreach"
)

func main() {
	// Two chains: 0->1->2 and 3->4->5.
	idx := dagreach.NewIndexer(6)
	for i := 0; i < 6; i++ {
		idx.AddNode()
	}
	for _, e := range [][2]int{{0, 1}, {1, 2}, {3, 4}, {4, 5}} {
		if err := idx.AddEdge(e[0], e[1]); err != nil {
			panic(err)
		}
	}

	fmt.Println("== layered DAG: chains 0->1->2 and 3->4->5 ==")
	printLevels(idx)

	fmt.Println("\n== O(1) reachability queries ==")
	for _, q := range [][2]int{{0, 2}, {3, 5}, {0, 5}, {2, 0}} {
		fmt.Printf("reachable(%d, %d) = %v\n", q[0], q[1], idx.Reachable(q[0], q[1]))
	}

	fmt.Println("\n== insert cross-layer edge 1->4 ==")
	fmt.Printf("before: reachable(0, 4) = %v, reachable(1, 5) = %v\n",
		idx.Reachable(0, 4), idx.Reachable(1, 5))
	if err := idx.AddEdge(1, 4); err != nil {
		panic(err)
	}
	fmt.Printf("after:  reachable(0, 4) = %v, reachable(1, 5) = %v (bitmap OR-propagated to ancestors)\n",
		idx.Reachable(0, 4), idx.Reachable(1, 5))
	fmt.Println("levels after relabeling downstream of node 4:")
	printLevels(idx)

	fmt.Println("\n== cycle protection ==")
	if err := idx.AddEdge(5, 0); err != nil {
		fmt.Printf("AddEdge(5, 0) rejected: %v\n", err)
	}

	fmt.Println("\n== memory on a sparse 5000-node DAG ==")
	big := dagreach.NewIndexer(5000)
	for i := 0; i < 5000; i++ {
		big.AddNode()
	}
	rng := rand.New(rand.NewSource(1))
	for u := 0; u+1 < 5000; u++ {
		for k := 0; k < 3; k++ {
			v := u + 1 + rng.Intn(5000-1-u)
			if err := big.AddEdge(u, v); err != nil {
				panic(err)
			}
		}
	}
	comp := big.Compress()
	dense := big.MemoryBytes()
	fmt.Printf("nodes=%d edges=%d\n", big.Size(), big.EdgeCount())
	fmt.Printf("dense bitmap:      %d bytes\n", dense)
	fmt.Printf("compressed bitmap: %d bytes (%.2fx smaller)\n",
		comp.MemoryBytes(), comp.CompressionRatio(dense))
	fmt.Printf("compressed query agrees with dense: %v\n",
		comp.Reachable(0, 4999) == big.Reachable(0, 4999))
}

func printLevels(idx *dagreach.Indexer) {
	for i := 0; i < idx.Size(); i++ {
		fmt.Printf("node %d: topological level %d\n", i, idx.Level(i))
	}
}
