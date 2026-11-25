package admission

import (
	"bytes"
	"fmt"
	"testing"
)

func admissionTestLeaves(n int) [][]byte {
	leaves := make([][]byte, n)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("leaf-%d", i))
	}
	return leaves
}

func TestImplementationMerkleNonFullTreeAndProofs(t *testing.T) {
	for n := 1; n <= 17; n++ {
		leaves := admissionTestLeaves(n)
		tree := NewTree(leaves)
		for index := 0; index < n; index++ {
			proof, err := tree.InclusionProof(uint64(index))
			if err != nil {
				t.Fatalf("size=%d index=%d: %v", n, index, err)
			}
			if !VerifyInclusion(leaves[index], uint64(index), uint64(n), proof, tree.Root()) {
				t.Fatalf("size=%d index=%d: inclusion proof failed", n, index)
			}
			if n > 1 {
				bad := append([][]byte(nil), proof...)
				if len(bad) == 0 {
					bad = [][]byte{make([]byte, HashSize)}
				}
				bad[0] = append([]byte(nil), bad[0]...)
				bad[0][0] ^= 1
				if VerifyInclusion(leaves[index], uint64(index), uint64(n), bad, tree.Root()) {
					t.Fatalf("size=%d index=%d: tampered inclusion proof accepted", n, index)
				}
			}
		}

		for oldSize := 1; oldSize <= n; oldSize++ {
			for newSize := oldSize; newSize <= n; newSize++ {
				proof, err := tree.ConsistencyProof(uint64(oldSize), uint64(newSize))
				if err != nil {
					t.Fatalf("size=%d old=%d new=%d: %v", n, oldSize, newSize, err)
				}
				oldRoot := NewTree(leaves[:oldSize]).Root()
				newRoot := NewTree(leaves[:newSize]).Root()
				if !VerifyConsistency(uint64(oldSize), uint64(newSize), oldRoot, newRoot, proof) {
					t.Fatalf("size=%d old=%d new=%d: consistency failed: %x", n, oldSize, newSize, proof)
				}
			}
		}
		for newSize := 1; newSize <= n; newSize++ {
			proof, err := tree.ConsistencyProof(0, uint64(newSize))
			if err != nil || len(proof) != 0 {
				t.Fatalf("size=%d empty-to-%d proof=%v err=%v", n, newSize, proof, err)
			}
			if !VerifyConsistency(0, uint64(newSize), EmptyRoot(), NewTree(leaves[:newSize]).Root(), proof) {
				t.Fatalf("size=%d empty-to-%d consistency failed", n, newSize)
			}
		}
	}
}

func TestImplementationConsistencyProofShortening(t *testing.T) {
	leaves := admissionTestLeaves(8)
	tree := NewTree(leaves)
	old := NewTree(leaves[:4]).Root()
	newRoot := tree.Root()
	proof, err := tree.ConsistencyProof(4, 8)
	if err != nil {
		t.Fatal(err)
	}

	// The old-root-prefixed form is redundant but safe because the verifier
	// already knows old.
	expanded := append([][]byte{old}, proof...)
	if !VerifyConsistency(4, 8, old, newRoot, expanded) {
		t.Fatal("old-root-prefixed shortened proof was rejected")
	}

	// A genuinely shortened proof omits the known old root. The canonical
	// proof still verifies.
	if !VerifyConsistency(4, 8, old, newRoot, proof) {
		t.Fatal("canonical consistency proof was rejected")
	}
	if VerifyConsistency(4, 8, old, newRoot, nil) {
		t.Fatal("empty consistency proof accepted for different sizes")
	}
}

func TestImplementationSplitUsesLargestPowerOfTwoBelowLength(t *testing.T) {
	cases := map[int]int{2: 1, 3: 2, 4: 2, 5: 4, 7: 4, 8: 4, 9: 8, 15: 8, 16: 8}
	for n, want := range cases {
		if got := splitPoint(n); got != want {
			t.Fatalf("splitPoint(%d)=%d want %d", n, got, want)
		}
	}

	// Explicitly verify the non-full split for a five-leaf tree.
	leaves := admissionTestLeaves(5)
	tree := NewTree(leaves)
	want := HashNode(MerkleRoot(leaves[:4]), HashLeaf(leaves[4]))
	if !bytes.Equal(tree.Root(), want) {
		t.Fatalf("five-leaf root does not use 4+1 split")
	}
}
