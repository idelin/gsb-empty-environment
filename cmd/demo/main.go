// Command demo builds a compressed graph index, prints its real byte
// layout, runs membership / intersection / degree / multiplicity queries,
// round-trips through Marshal/Parse, and shows that a corrupted delta
// (decoding to an out-of-range node id) is rejected at load time.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"

	"graphindex"
)

func main() {
	const n = 100000
	const edges = 1000000

	b := graphindex.NewBuilder(n)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < edges; i++ {
		u := uint32(rng.Intn(n))
		v := uint32(rng.Intn(n))
		if err := b.AddEdge(u, v); err != nil {
			panic(err)
		}
	}
	// Hand-picked cases: self-loop and heavy parallel edges.
	for k := 0; k < 3; k++ {
		must(b.AddEdge(5, 5)) // self-loop x3
	}
	for k := 0; k < 10; k++ {
		must(b.AddEdge(5, 77)) // parallel edges x10
	}
	must(b.AddEdge(5, 9001))
	must(b.AddEdge(9, 77))
	must(b.AddEdge(9, 9001))
	must(b.AddEdge(9, 5))

	g := b.Build()
	if err := g.Validate(); err != nil {
		panic(err)
	}

	st := g.Stats()
	fmt.Printf("nodes=%d uniqueEdges=%d edgeInstances=%d\n", st.NumNodes, st.UniqueEdges, st.EdgeInstances)
	fmt.Println("--- byte layout ---")
	fmt.Printf("offset arrays (2 x (n+1) x 8B): %10d B\n", st.OffsetBytes)
	fmt.Printf("node headers (deg,nskips):    %10d B\n", st.HeaderBytes)
	fmt.Printf("skip tables (absID+offset):   %10d B\n", st.SkipBytes)
	fmt.Printf("delta payloads:               %10d B\n", st.PayloadBytes)
	fmt.Printf("multiplicity sidecar:         %10d B\n", st.MultBytes)
	fmt.Printf("TOTAL:                        %10d B\n", st.TotalBytes)
	fmt.Printf("naive CSR (4B/neighbor):      %10d B  (%.2fx larger)\n",
		st.NaiveBytes, float64(st.NaiveBytes)/float64(st.TotalBytes))

	fmt.Println("--- queries ---")
	d, _ := g.Degree(5)
	w, _ := g.WeightedDegree(5)
	fmt.Printf("Degree(5)=%d distinct, WeightedDegree(5)=%d instances\n", d, w)
	m, _ := g.Multiplicity(5, 77)
	fmt.Printf("Multiplicity(5,77)=%d (10 parallel edges)\n", m)
	m, _ = g.Multiplicity(5, 5)
	fmt.Printf("Multiplicity(5,5)=%d (self-loop x3, preserved)\n", m)
	ok, _ := g.HasEdge(5, 9001)
	fmt.Printf("HasEdge(5,9001)=%v\n", ok)
	ok, _ = g.HasEdge(77, 5)
	fmt.Printf("HasEdge(77,5)=%v (directed, reverse not stored)\n", ok)
	inter, _ := g.Intersect(5, 9)
	fmt.Printf("Intersect(5,9)=%v\n", inter)

	fmt.Println("--- marshal / parse round-trip ---")
	buf := g.Marshal()
	g2, err := graphindex.Parse(buf)
	if err != nil {
		panic(err)
	}
	d2, _ := g2.Degree(5)
	fmt.Printf("serialized=%d B, re-parsed ok, Degree(5)=%d\n", len(buf), d2)

	fmt.Println("--- corruption rejection ---")
	bad := graphindex.NewBuilder(16)
	must(bad.AddEdge(0, 3))
	must(bad.AddEdge(0, 5))
	must(bad.AddEdge(0, 8))
	badBuf := bad.Build().Marshal()
	// Locate the first payload delta of node 0 inside the blob:
	// magic(4) | numNodes | dataLen | [deg nskips absID offset] delta...
	p := len("GIDX")
	_, w1 := binary.Uvarint(badBuf[p:])
	p += w1
	_, w2 := binary.Uvarint(badBuf[p:])
	p += w2
	p += 4           // node 0 header: deg, nskips, skip absID, skip offset
	badBuf[p] = 0x7f // delta 127 -> id 3+127 = 130 >= numNodes(16)
	_, err = graphindex.Parse(badBuf)
	fmt.Printf("Parse() with corrupted delta: err=%v\n", err)
	fmt.Printf("errors.Is(err, ErrCorrupt)=%v\n", errors.Is(err, graphindex.ErrCorrupt))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
