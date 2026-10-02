// Command demo builds an 8-leaf Merkle tree, generates a compressed
// multi-proof for leaves #3, #4, #7 and #8, and compares its size against
// the sum of the four independent proofs.
package main

import (
	"fmt"

	"multiproof"
)

func main() {
	leaves := make([][]byte, 8)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("leaf-%d", i+1))
	}
	tree := multiproof.NewTree(leaves)
	fmt.Printf("Merkle tree: %d leaves, depth %d\n", tree.LeafCount(), tree.Depth())
	fmt.Printf("root: %x\n\n", tree.Root())

	// 1-based leaf numbers 3, 4, 7, 8 -> 0-based indices 2, 3, 6, 7.
	indices := []int{2, 3, 6, 7}
	fmt.Println("proving leaves #3, #4, #7, #8")

	independent := tree.IndependentProofSize(indices)
	fmt.Printf("4 independent proofs: %d sibling hashes total\n", independent)

	mp, err := tree.GenerateMultiProof(indices)
	if err != nil {
		panic(err)
	}
	fmt.Printf("compressed multi-proof: %d hashes (deduplicated DAG)\n", mp.Size())
	for i, h := range mp.ProofHashes {
		fmt.Printf("  proof hash %d: %x...\n", i, h[:8])
	}

	reduction := 1 - float64(mp.Size())/float64(independent)
	fmt.Printf("size reduction: %.1f%%\n\n", reduction*100)

	if multiproof.VerifyMultiProof(tree.Root(), mp) {
		fmt.Println("verification: OK (single pass over the DAG proof)")
	} else {
		fmt.Println("verification: FAILED")
	}
}
