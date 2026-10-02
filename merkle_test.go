package multiproof

import (
	"fmt"
	"testing"
)

func buildTestTree(n int) *Tree {
	leaves := make([][]byte, n)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("leaf-%d", i+1)) // 1-based labels
	}
	return NewTree(leaves)
}

func TestSingleProofRoundTrip(t *testing.T) {
	tree := buildTestTree(8)
	for i := 0; i < 8; i++ {
		p, err := tree.GenerateProof(i)
		if err != nil {
			t.Fatalf("GenerateProof(%d): %v", i, err)
		}
		if !VerifyProof(tree.Root(), p) {
			t.Fatalf("proof for leaf %d did not verify", i)
		}
	}
}

// Acceptance case: leaves 3, 4, 7, 8 (1-based) in an 8-leaf tree.
func TestMultiProofAcceptanceCase(t *testing.T) {
	tree := buildTestTree(8)
	indices := []int{2, 3, 6, 7} // leaves #3, #4, #7, #8 (0-based)

	mp, err := tree.GenerateMultiProof(indices)
	if err != nil {
		t.Fatalf("GenerateMultiProof: %v", err)
	}
	if !VerifyMultiProof(tree.Root(), mp) {
		t.Fatal("multi-proof did not verify")
	}

	independent := tree.IndependentProofSize(indices) // 4 leaves * 3 levels = 12
	compressed := mp.Size()
	reduction := 1 - float64(compressed)/float64(independent)
	t.Logf("independent proofs: %d hashes, multi-proof: %d hashes, reduction: %.1f%%",
		independent, compressed, reduction*100)
	if reduction < 0.5 {
		t.Fatalf("compression below 50%%: got %.1f%%", reduction*100)
	}
}

func TestMultiProofIsDeduplicatedDAG(t *testing.T) {
	tree := buildTestTree(8)
	// Leaves #3,#4 share one parent; #7,#8 share another. The multi-proof
	// must contain only the two uncle hashes at level 1, nothing else.
	mp, err := tree.GenerateMultiProof([]int{2, 3, 6, 7})
	if err != nil {
		t.Fatal(err)
	}
	if mp.Size() != 2 {
		t.Fatalf("expected 2 deduplicated hashes, got %d", mp.Size())
	}
}

func TestMultiProofRejectsTampering(t *testing.T) {
	tree := buildTestTree(8)
	mp, err := tree.GenerateMultiProof([]int{2, 3, 6, 7})
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]func(*MultiProof){
		"wrong root": func(m *MultiProof) {},
		"flipped proof hash": func(m *MultiProof) {
			m.ProofHashes[0][0] ^= 0xff
		},
		"flipped leaf hash": func(m *MultiProof) {
			m.LeafHashes[1][0] ^= 0xff
		},
		"truncated proof": func(m *MultiProof) {
			m.ProofHashes = m.ProofHashes[:len(m.ProofHashes)-1]
		},
		"extra proof hash": func(m *MultiProof) {
			m.ProofHashes = append(m.ProofHashes, Hash{})
		},
		"unsorted indices": func(m *MultiProof) {
			m.Indices[0], m.Indices[1] = m.Indices[1], m.Indices[0]
			m.LeafHashes[0], m.LeafHashes[1] = m.LeafHashes[1], m.LeafHashes[0]
		},
	}
	for name, mutate := range cases {
		bad := *mp
		bad.Indices = append([]int{}, mp.Indices...)
		bad.LeafHashes = append([]Hash{}, mp.LeafHashes...)
		bad.ProofHashes = append([]Hash{}, mp.ProofHashes...)
		mutate(&bad)
		root := tree.Root()
		if name == "wrong root" {
			root[0] ^= 0xff
		}
		if VerifyMultiProof(root, &bad) {
			t.Fatalf("tampered proof (%s) verified", name)
		}
	}
}

func TestMultiProofEdgeCases(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 8, 13} {
		tree := buildTestTree(n)
		// All leaves.
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		mp, err := tree.GenerateMultiProof(all)
		if err != nil {
			t.Fatalf("n=%d all leaves: %v", n, err)
		}
		if !VerifyMultiProof(tree.Root(), mp) {
			t.Fatalf("n=%d: all-leaf multi-proof failed", n)
		}
		// With a power-of-two leaf count, proving every leaf needs no
		// proof hashes at all; odd trees still need padding hashes.
		if n&(n-1) == 0 && mp.Size() != 0 {
			t.Fatalf("n=%d: proving all leaves should need 0 hashes, got %d", n, mp.Size())
		}
		// Single leaf behaves like an independent proof.
		mp1, err := tree.GenerateMultiProof([]int{n - 1})
		if err != nil {
			t.Fatalf("n=%d single leaf: %v", n, err)
		}
		if !VerifyMultiProof(tree.Root(), mp1) {
			t.Fatalf("n=%d: single-leaf multi-proof failed", n)
		}
		if mp1.Size() != tree.Depth() {
			t.Fatalf("n=%d: single-leaf proof size %d, want depth %d", n, mp1.Size(), tree.Depth())
		}
	}
}

func TestMultiProofInputValidation(t *testing.T) {
	tree := buildTestTree(8)
	if _, err := tree.GenerateMultiProof(nil); err == nil {
		t.Fatal("expected error for empty index set")
	}
	if _, err := tree.GenerateMultiProof([]int{1, 1}); err == nil {
		t.Fatal("expected error for duplicate indices")
	}
	if _, err := tree.GenerateMultiProof([]int{8}); err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestCompressionScalesWithTreeSize(t *testing.T) {
	for _, n := range []int{8, 16, 64, 256} {
		tree := buildTestTree(n)
		// Prove n/2 evenly spread leaves.
		var indices []int
		for i := 0; i < n; i += 2 {
			indices = append(indices, i)
		}
		mp, err := tree.GenerateMultiProof(indices)
		if err != nil {
			t.Fatal(err)
		}
		if !VerifyMultiProof(tree.Root(), mp) {
			t.Fatalf("n=%d: verification failed", n)
		}
		independent := tree.IndependentProofSize(indices)
		reduction := 1 - float64(mp.Size())/float64(independent)
		t.Logf("n=%d leaves=%d: independent=%d compressed=%d reduction=%.1f%%",
			n, len(indices), independent, mp.Size(), reduction*100)
		if reduction < 0.5 {
			t.Fatalf("n=%d: reduction %.1f%% below 50%%", n, reduction*100)
		}
	}
}
