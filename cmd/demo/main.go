// Demo: build a compressed graph index, print its real byte layout,
// run membership / intersection / degree queries, and show that a
// corrupted delta is rejected.
package main

import (
	"fmt"
	"math/rand"

	"cgraph"
)

func main() {
	const n = 10000
	rng := rand.New(rand.NewSource(42))

	edges := make([]cgraph.Edge, 0, 210000)
	for i := 0; i < 200000; i++ {
		edges = append(edges, cgraph.Edge{From: uint32(rng.Intn(n)), To: uint32(rng.Intn(n))})
	}
	for i := 0; i < 1000; i++ { // parallel edges: duplicate some edges twice
		e := edges[rng.Intn(len(edges))]
		edges = append(edges, e, e)
	}
	for u := uint32(0); u < 50; u++ { // self-loops
		edges = append(edges, cgraph.Edge{From: u, To: u})
	}
	for u := uint32(9000); u < 9012; u++ { // clique: guarantees a non-empty intersection
		for v := u + 1; v < 9012; v++ {
			edges = append(edges, cgraph.Edge{From: u, To: v})
		}
	}

	g, err := cgraph.Build(n, edges)
	if err != nil {
		panic(err)
	}
	if err := g.Validate(); err != nil {
		panic(err)
	}

	var distinct, degree uint64
	for u := uint32(0); u < n; u++ {
		c, _ := g.DistinctNeighbors(u)
		d, _ := g.Degree(u)
		distinct += uint64(c)
		degree += d
	}

	fmt.Println("=== build ===")
	fmt.Printf("nodes=%d  input edges=%d  directed adjacency entries=%d (distinct %d)\n",
		n, len(edges), degree, distinct)

	fmt.Println("=== size (real bytes) ===")
	fmt.Printf("delta-encoded buffer : %9d bytes  (%.2f B/distinct entry)\n",
		g.EncodedBytes(), float64(g.EncodedBytes())/float64(distinct))
	fmt.Printf("skip index (1/%d)    : %9d bytes\n", cgraph.SkipInterval, g.SkipIndexBytes())
	fmt.Printf("node directory       : %9d bytes\n", g.NodeIndexBytes())
	fmt.Printf("total                : %9d bytes\n", g.TotalBytes())
	fmt.Printf("raw []uint32 baseline: %9d bytes  (4 B/entry, multiplicities not even stored)\n",
		4*distinct)

	fmt.Println("=== queries ===")
	d, _ := g.Degree(7)
	fmt.Printf("degree(7) = %d\n", d)
	ok, _ := g.HasEdge(7, 7)
	fmt.Printf("hasEdge(7,7) [self-loop] = %v\n", ok)
	e := edges[12345]
	m, _ := g.Multiplicity(e.From, e.To)
	fmt.Printf("multiplicity(%d,%d) = %d\n", e.From, e.To, m)
	common, _ := g.Intersection(9000, 9001)
	fmt.Printf("intersection(9000,9001) = %v (%d common neighbors)\n", common, len(common))

	fmt.Println("=== corruption rejection ===")
	small, _ := cgraph.Build(8, []cgraph.Edge{{From: 0, To: 1}, {From: 0, To: 3}, {From: 0, To: 5}})
	buf := small.RawBuffer()
	fmt.Printf("node 0 section before: % x\n", buf[:8])
	buf[2] = 100 // first delta now decodes to id 100, but only 8 nodes exist
	fmt.Printf("node 0 section after : % x\n", buf[:8])
	if err := small.Validate(); err != nil {
		fmt.Println("Validate ->", err)
	}
	if _, _, err := small.Neighbors(0); err != nil {
		fmt.Println("Neighbors ->", err)
	}
}
