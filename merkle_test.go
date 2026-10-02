package merklemulti

import (
	"fmt"
	"math/rand"
	"testing"
)

func makeLeaves(n int) [][]byte {
	leaves := make([][]byte, n)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("leaf-%d", i))
	}
	return leaves
}

// independentProofSize sums the hash count (leaf hash + siblings) of the
// independent proofs for the given indices.
func independentProofSize(t *testing.T, tree *Tree, indices []int) int {
	t.Helper()
	total := 0
	for _, idx := range indices {
		p, err := tree.Prove(idx)
		if err != nil {
			t.Fatalf("Prove(%d): %v", idx, err)
		}
		if !VerifyProof(tree.Root(), p) {
			t.Fatalf("independent proof for leaf %d does not verify", idx)
		}
		total += 1 + len(p.Siblings)
	}
	return total
}

func TestSingleProofs(t *testing.T) {
	tree := NewTree(makeLeaves(16))
	for i := 0; i < 16; i++ {
		p, err := tree.Prove(i)
		if err != nil {
			t.Fatalf("Prove(%d): %v", i, err)
		}
		if !VerifyProof(tree.Root(), p) {
			t.Errorf("proof for leaf %d failed verification", i)
		}
	}
}

// TestAcceptanceLeaves3_4_7_8 is the acceptance scenario: batch-prove leaves
// 3, 4, 7 and 8, merge the overlapping paths into one DAG proof, and require
// a size reduction of more than 50% versus the independent proofs.
func TestAcceptanceLeaves3_4_7_8(t *testing.T) {
	tree := NewTree(makeLeaves(128))
	indices := []int{3, 4, 7, 8}

	mp, err := tree.ProveMulti(indices)
	if err != nil {
		t.Fatalf("ProveMulti: %v", err)
	}
	if !VerifyMulti(tree.Root(), mp) {
		t.Fatal("multi-proof failed verification")
	}

	independent := independentProofSize(t, tree, indices)
	compressed := mp.Size()
	reduction := 1 - float64(compressed)/float64(independent)
	t.Logf("independent proofs: %d hashes, multi-proof: %d hashes, reduction: %.1f%%",
		independent, compressed, reduction*100)
	if reduction <= 0.5 {
		t.Fatalf("reduction %.1f%% does not exceed 50%%", reduction*100)
	}
}

func TestMultiProofRandomSubsets(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, numLeaves := range []int{1, 2, 3, 5, 8, 16, 17, 64, 100} {
		tree := NewTree(makeLeaves(numLeaves))
		for trial := 0; trial < 50; trial++ {
			k := 1 + rng.Intn(tree.NumLeaves())
			perm := rng.Perm(tree.NumLeaves())[:k] // random subset, unsorted on purpose
			mp, err := tree.ProveMulti(perm)
			if err != nil {
				t.Fatalf("leaves=%d subset=%v: %v", numLeaves, perm, err)
			}
			if !VerifyMulti(tree.Root(), mp) {
				t.Fatalf("leaves=%d subset=%v: verification failed", numLeaves, perm)
			}
			independent := independentProofSize(t, tree, perm)
			if mp.Size() > independent {
				t.Fatalf("leaves=%d subset=%v: multi-proof (%d) larger than independent (%d)",
					numLeaves, perm, mp.Size(), independent)
			}
		}
	}
}

func TestMultiProofTampering(t *testing.T) {
	tree := NewTree(makeLeaves(32))
	mp, err := tree.ProveMulti([]int{3, 4, 7, 8})
	if err != nil {
		t.Fatal(err)
	}

	// Wrong root.
	var badRoot Hash
	badRoot[0] = 0xff
	if VerifyMulti(badRoot, mp) {
		t.Error("accepted proof against wrong root")
	}
	// Flipped leaf hash.
	bad := *mp
	bad.LeafHashes = append([]Hash(nil), mp.LeafHashes...)
	bad.LeafHashes[1][0] ^= 0xff
	if VerifyMulti(tree.Root(), &bad) {
		t.Error("accepted tampered leaf hash")
	}
	// Flipped proof hash.
	bad = *mp
	bad.ProofHashes = append([]Hash(nil), mp.ProofHashes...)
	bad.ProofHashes[0][0] ^= 0xff
	if VerifyMulti(tree.Root(), &bad) {
		t.Error("accepted tampered proof hash")
	}
	// Truncated proof.
	bad = *mp
	bad.ProofHashes = mp.ProofHashes[:len(mp.ProofHashes)-1]
	if VerifyMulti(tree.Root(), &bad) {
		t.Error("accepted truncated proof")
	}
	// Extra junk hash appended.
	bad = *mp
	bad.ProofHashes = append(append([]Hash(nil), mp.ProofHashes...), Hash{})
	if VerifyMulti(tree.Root(), &bad) {
		t.Error("accepted proof with trailing junk hash")
	}
	// Unsorted leaf indices.
	bad = *mp
	bad.LeafIndices = []int{8, 7, 4, 3}
	if VerifyMulti(tree.Root(), &bad) {
		t.Error("accepted unsorted leaf indices")
	}
}

func TestProveMultiErrors(t *testing.T) {
	tree := NewTree(makeLeaves(8))
	if _, err := tree.ProveMulti(nil); err == nil {
		t.Error("expected error for empty index list")
	}
	if _, err := tree.ProveMulti([]int{1, 1}); err == nil {
		t.Error("expected error for duplicate indices")
	}
	if _, err := tree.ProveMulti([]int{8}); err == nil {
		t.Error("expected error for out-of-range index")
	}
	if _, err := tree.ProveMulti([]int{-1}); err == nil {
		t.Error("expected error for negative index")
	}
	if _, err := tree.Prove(8); err == nil {
		t.Error("expected error for out-of-range single proof")
	}
}
