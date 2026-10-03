// Command lseq-demo runs the LSEQ acceptance scenario: dense alternating
// inserts at both document edges, then a concurrent multi-node editing
// session merged under random permutations.
package main

import (
	"fmt"
	"math/rand"

	"lseq"
)

func main() {
	fmt.Println("=== LSEQ demo: adaptive variable-base collaborative sequence ===")
	fmt.Println()

	// 1. Dense alternating inserts at the beginning and the end.
	s := lseq.New(1, rand.New(rand.NewSource(42)))
	const n = 1000
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			s.Insert(0, 'A')
		} else {
			s.Insert(s.Len(), 'Z')
		}
	}
	maxDepth, avgDepth := s.DepthStats()
	fmt.Printf("dense alternating inserts: %d edits -> len=%d maxDepth=%d avgDepth=%.2f sorted=%v\n",
		n, s.Len(), maxDepth, avgDepth, s.Sorted())

	// depth growth curve: keep typing at the tail and sample the depth
	g := lseq.New(2, rand.New(rand.NewSource(7)))
	fmt.Println("depth growth while typing 4096 chars at the tail:")
	for i := 1; i <= 4096; i++ {
		g.Insert(g.Len(), 'x')
		if i&(i-1) == 0 { // power of two
			md, ad := g.DepthStats()
			fmt.Printf("  n=%4d  maxDepth=%d avgDepth=%.2f\n", i, md, ad)
		}
	}
	fmt.Println()

	// 2. Concurrent multi-node editing merged in random order.
	words := []string{"the ", "quick ", "brown ", "fox"}
	const nodes = 4
	replicas := make([]*lseq.Sequence, nodes)
	var ops []lseq.Op
	for i := 0; i < nodes; i++ {
		replicas[i] = lseq.New(uint64(i+10), rand.New(rand.NewSource(int64(1000+i))))
		for _, r := range words[i] {
			ops = append(ops, replicas[i].Insert(replicas[i].Len(), r))
		}
	}
	rng := rand.New(rand.NewSource(2024))
	merged := lseq.New(99, nil)
	for _, j := range rng.Perm(len(ops)) {
		merged.Apply(ops[j])
	}
	fmt.Printf("concurrent merge of %d nodes (%d ops, random permutation):\n", nodes, len(ops))
	fmt.Printf("  document: %q\n", merged.String())
	fmt.Printf("  sorted=%v len=%d\n", merged.Sorted(), merged.Len())
}
