package cdc

import (
	"crypto/sha256"
	"testing"
)

func leafHashes(n int) [][32]byte {
	leaves := make([][32]byte, n)
	for i := 0; i < n; i++ {
		leaves[i] = sha256.Sum256([]byte{byte(i), byte(i >> 8)})
	}
	return leaves
}

func TestMerkleRootStable(t *testing.T) {
	leaves := leafHashes(100)
	if BuildTree(leaves).Root() != BuildTree(leaves).Root() {
		t.Fatal("same leaves produced different roots")
	}
	other := leafHashes(100)
	other[50][0] ^= 0xff
	if BuildTree(leaves).Root() == BuildTree(other).Root() {
		t.Fatal("flipping one leaf did not change the root")
	}
}

func TestMerkleProofAllLeaves(t *testing.T) {
	for _, n := range []int{1, 2, 3, 7, 8, 9, 100, 1000} {
		leaves := leafHashes(n)
		tree := BuildTree(leaves)
		root := tree.Root()
		for i := 0; i < n; i++ {
			if !VerifyProof(leaves[i], tree.Proof(i), root) {
				t.Fatalf("n=%d: proof for leaf %d does not verify", n, i)
			}
		}
	}
}

func TestMerkleProofDetectsTampering(t *testing.T) {
	leaves := leafHashes(64)
	tree := BuildTree(leaves)
	bad := leaves[10]
	bad[0] ^= 0x01
	if VerifyProof(bad, tree.Proof(10), tree.Root()) {
		t.Fatal("proof verified for tampered leaf")
	}
}

func TestEmptyTree(t *testing.T) {
	if BuildTree(nil) != nil {
		t.Fatal("expected nil tree for empty input")
	}
	var zero [32]byte
	if (*Tree)(nil).Root() != zero {
		t.Fatal("nil tree root is not the zero hash")
	}
}
