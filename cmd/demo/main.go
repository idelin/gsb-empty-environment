// Command demo builds a 128-leaf Merkle tree, batch-proves leaves 3, 4, 7
// and 8, and shows the compression achieved by the multi-proof versus four
// independent proofs.
package main

import (
	"fmt"

	"merklemulti"
)

func main() {
	leaves := make([][]byte, 128)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("leaf-%d", i))
	}
	tree := merklemulti.NewTree(leaves)
	root := tree.Root()
	fmt.Printf("tree: %d leaves, depth %d, root %x...\n",
		tree.NumLeaves(), tree.Depth(), root[:8])

	indices := []int{3, 4, 7, 8}

	independent := 0
	for _, idx := range indices {
		p, err := tree.Prove(idx)
		if err != nil {
			panic(err)
		}
		if !merklemulti.VerifyProof(tree.Root(), p) {
			panic("independent proof failed")
		}
		independent += 1 + len(p.Siblings)
	}

	mp, err := tree.ProveMulti(indices)
	if err != nil {
		panic(err)
	}
	fmt.Printf("leaves proven:        %v\n", mp.LeafIndices)
	fmt.Printf("independent proofs:   %d hashes (%d bytes)\n", independent, independent*32)
	fmt.Printf("compressed DAG proof: %d hashes (%d bytes)\n", mp.Size(), mp.Size()*32)
	fmt.Printf("  - leaf hashes:      %d\n", len(mp.LeafHashes))
	fmt.Printf("  - deduped siblings: %d\n", len(mp.ProofHashes))
	fmt.Printf("size reduction:       %.1f%%\n",
		100*(1-float64(mp.Size())/float64(independent)))

	if merklemulti.VerifyMulti(tree.Root(), mp) {
		fmt.Println("verification: OK (single bottom-up traversal)")
	} else {
		panic("multi-proof verification failed")
	}
}
