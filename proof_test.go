package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

// mockVRF is a stand-in for the caller-injected VRF verifier. The VRF
// output for a label is sha256("index:"+label) and the only accepted
// proof is "vrf-proof:"+label.
type mockVRF struct{}

func indexFor(label string) [HashSize]byte {
	return sha256.Sum256([]byte("index:" + label))
}

func validVRFProof(label string) []byte {
	return []byte("vrf-proof:" + label)
}

func (mockVRF) Verify(label, proof []byte) ([]byte, error) {
	if !bytes.Equal(proof, validVRFProof(string(label))) {
		return nil, errors.New("mockVRF: invalid proof")
	}
	index := indexFor(string(label))
	return index[:], nil
}

// treeSpec is a set of leaves for the reference tree builder.
type treeSpec map[[HashSize]byte][]byte

func splitLeaves(leaves treeSpec, depth int) (zero, one treeSpec) {
	zero, one = treeSpec{}, treeSpec{}
	for index, value := range leaves {
		if bit(index[:], depth) == 0 {
			zero[index] = value
		} else {
			one[index] = value
		}
	}
	return zero, one
}

// nodeHash builds a compressed prefix tree: a subtree holding a single
// leaf is stored as that leaf, an empty subtree as the empty hash.
func nodeHash(leaves treeSpec, depth int) []byte {
	switch len(leaves) {
	case 0:
		return hashEmpty(depth)
	case 1:
		for index, value := range leaves {
			return hashLeaf(depth, index[:], value)
		}
	}
	zero, one := splitLeaves(leaves, depth)
	return hashNode(depth, nodeHash(zero, depth+1), nodeHash(one, depth+1))
}

// buildProof walks the query index down the tree, collecting siblings,
// and returns the terminal leaf (nil for an empty subtree).
func buildProof(leaves treeSpec, query [HashSize]byte) (siblings [][]byte, leaf *Leaf) {
	current := leaves
	for depth := 0; ; depth++ {
		switch len(current) {
		case 0:
			return siblings, nil
		case 1:
			for index, value := range current {
				return siblings, &Leaf{Index: index, Value: value}
			}
		}
		zero, one := splitLeaves(current, depth)
		if bit(query[:], depth) == 0 {
			siblings = append(siblings, nodeHash(one, depth+1))
			current = zero
		} else {
			siblings = append(siblings, nodeHash(zero, depth+1))
			current = one
		}
	}
}

func testTree() treeSpec {
	return treeSpec{
		indexFor("alice"): []byte("alice-pubkey"),
		indexFor("bob"):   []byte("bob-pubkey"),
		indexFor("carol"): []byte("carol-pubkey"),
		indexFor("dave"):  []byte("dave-pubkey"),
	}
}

func inclusionProof(t *testing.T, leaves treeSpec, label string) (*Proof, []byte) {
	t.Helper()
	siblings, leaf := buildProof(leaves, indexFor(label))
	if leaf == nil {
		t.Fatalf("no inclusion proof for %q", label)
	}
	return &Proof{
		Label:    []byte(label),
		VRFProof: validVRFProof(label),
		Leaf:     leaf,
		Siblings: siblings,
	}, nodeHash(leaves, 0)
}

// absentQuery returns a query label whose VRF output is not in the tree
// and whose proof terminates as requested: in an empty subtree or in a
// different leaf.
func absentQuery(t *testing.T, leaves treeSpec, wantLeaf bool) (string, *Proof, []byte) {
	t.Helper()
	for i := 0; ; i++ {
		label := fmt.Sprintf("query-%d", i)
		siblings, leaf := buildProof(leaves, indexFor(label))
		if (leaf != nil) == wantLeaf {
			return label, &Proof{
				Label:    []byte(label),
				VRFProof: validVRFProof(label),
				Leaf:     leaf,
				Siblings: siblings,
			}, nodeHash(leaves, 0)
		}
	}
}

func TestVerifyInclusion(t *testing.T) {
	leaves := testTree()
	for _, label := range []string{"alice", "bob", "carol", "dave"} {
		proof, root := inclusionProof(t, leaves, label)
		value, err := VerifyInclusion(mockVRF{}, root, proof)
		if err != nil {
			t.Fatalf("inclusion for %q rejected: %v", label, err)
		}
		if want := []byte(label + "-pubkey"); !bytes.Equal(value, want) {
			t.Fatalf("inclusion for %q returned %q, want %q", label, value, want)
		}
	}
}

func TestVerifyNonInclusion(t *testing.T) {
	leaves := testTree()

	label, proof, root := absentQuery(t, leaves, false)
	if err := VerifyNonInclusion(mockVRF{}, root, proof); err != nil {
		t.Fatalf("empty-subtree non-inclusion for %q rejected: %v", label, err)
	}

	label, proof, root = absentQuery(t, leaves, true)
	if err := VerifyNonInclusion(mockVRF{}, root, proof); err != nil {
		t.Fatalf("different-leaf non-inclusion for %q rejected: %v", label, err)
	}
}

func TestLeafSwap(t *testing.T) {
	leaves := testTree()
	proof, root := inclusionProof(t, leaves, "alice")

	// Swap in another leaf's key material.
	forged := *proof
	forgedLeaf := *proof.Leaf
	forgedLeaf.Value = []byte("bob-pubkey")
	forged.Leaf = &forgedLeaf
	if _, err := VerifyInclusion(mockVRF{}, root, &forged); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("swapped leaf value: got %v, want ErrRootMismatch", err)
	}

	// Swap in another leaf's index: it no longer equals the VRF output.
	forged = *proof
	forgedLeaf = *proof.Leaf
	forgedLeaf.Index = indexFor("bob")
	forged.Leaf = &forgedLeaf
	if _, err := VerifyInclusion(mockVRF{}, root, &forged); !errors.Is(err, ErrLeafIndexMismatch) {
		t.Fatalf("swapped leaf index: got %v, want ErrLeafIndexMismatch", err)
	}
}

func TestBitOrderConfusion(t *testing.T) {
	leaves := testTree()

	// Find the deepest proof so there are at least two siblings.
	var proof *Proof
	var root []byte
	for _, label := range []string{"alice", "bob", "carol", "dave"} {
		p, r := inclusionProof(t, leaves, label)
		if proof == nil || len(p.Siblings) > len(proof.Siblings) {
			proof, root = p, r
		}
	}
	if len(proof.Siblings) < 2 {
		t.Fatalf("test tree too shallow: deepest proof has %d siblings", len(proof.Siblings))
	}

	// Reverse the sibling order.
	forged := *proof
	forged.Siblings = make([][]byte, len(proof.Siblings))
	for i, s := range proof.Siblings {
		forged.Siblings[len(proof.Siblings)-1-i] = s
	}
	if _, err := VerifyInclusion(mockVRF{}, root, &forged); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("reversed siblings: got %v, want ErrRootMismatch", err)
	}

	// Swap two adjacent siblings.
	forged = *proof
	forged.Siblings = append([][]byte(nil), proof.Siblings...)
	forged.Siblings[0], forged.Siblings[1] = forged.Siblings[1], forged.Siblings[0]
	if _, err := VerifyInclusion(mockVRF{}, root, &forged); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("swapped siblings: got %v, want ErrRootMismatch", err)
	}
}

func TestDepthConfusion(t *testing.T) {
	leaves := testTree()
	proof, root := inclusionProof(t, leaves, "alice")

	// Domain and depth separation sanity checks.
	index := indexFor("alice")
	if bytes.Equal(hashLeaf(1, index[:], []byte("v")), hashLeaf(2, index[:], []byte("v"))) {
		t.Fatal("leaf hash ignores depth")
	}
	if bytes.Equal(hashEmpty(1), hashEmpty(2)) {
		t.Fatal("empty hash ignores depth")
	}
	if bytes.Equal(hashLeaf(1, index[:], []byte("v")), hashEmpty(1)) {
		t.Fatal("leaf and empty hashes share a domain")
	}

	// Truncate the path: the leaf is replayed one level up.
	forged := *proof
	forged.Siblings = proof.Siblings[:len(proof.Siblings)-1]
	if _, err := VerifyInclusion(mockVRF{}, root, &forged); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("truncated path: got %v, want ErrRootMismatch", err)
	}

	// Extend the path: the leaf is pushed one level down.
	forged = *proof
	forged.Siblings = append(append([][]byte(nil), proof.Siblings...), hashEmpty(len(proof.Siblings)+1))
	if _, err := VerifyInclusion(mockVRF{}, root, &forged); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("extended path: got %v, want ErrRootMismatch", err)
	}
}

func TestFakeNonInclusion(t *testing.T) {
	leaves := testTree()

	// The server claims an existing label is absent, presenting its own
	// leaf as the "different" leaf of a non-inclusion proof.
	inclusion, root := inclusionProof(t, leaves, "alice")
	fake := &Proof{
		Label:    inclusion.Label,
		VRFProof: inclusion.VRFProof,
		Leaf:     inclusion.Leaf,
		Siblings: inclusion.Siblings,
	}
	if err := VerifyNonInclusion(mockVRF{}, root, fake); !errors.Is(err, ErrLeafMatchesVRF) {
		t.Fatalf("presented existing leaf: got %v, want ErrLeafMatchesVRF", err)
	}

	// The server claims the path ends in an empty subtree.
	fake = &Proof{
		Label:    inclusion.Label,
		VRFProof: inclusion.VRFProof,
		Leaf:     nil,
		Siblings: inclusion.Siblings,
	}
	if err := VerifyNonInclusion(mockVRF{}, root, fake); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("claimed empty subtree: got %v, want ErrRootMismatch", err)
	}

	// The server presents a leaf that does not share the walked prefix.
	_, different, _ := absentQuery(t, leaves, true)
	if len(different.Siblings) < 1 {
		t.Fatal("different-leaf proof has no siblings")
	}
	stranger := *different.Leaf
	stranger.Index[0] ^= 0x80 // flip the first bit, breaking the prefix
	fake = &Proof{
		Label:    different.Label,
		VRFProof: different.VRFProof,
		Leaf:     &stranger,
		Siblings: different.Siblings,
	}
	if err := VerifyNonInclusion(mockVRF{}, root, fake); !errors.Is(err, ErrPrefixMismatch) {
		t.Fatalf("prefix-breaking leaf: got %v, want ErrPrefixMismatch", err)
	}
}

func TestEpochRollback(t *testing.T) {
	leaves := testTree()
	proof, root := inclusionProof(t, leaves, "alice")
	v := &Verifier{VRF: mockVRF{}}

	if _, err := v.VerifyInclusion(10, root, proof); err != nil {
		t.Fatalf("epoch 10 rejected: %v", err)
	}
	if _, err := v.VerifyInclusion(10, root, proof); !errors.Is(err, ErrEpochRegression) {
		t.Fatalf("replayed epoch 10: got %v, want ErrEpochRegression", err)
	}
	if _, err := v.VerifyInclusion(9, root, proof); !errors.Is(err, ErrEpochRegression) {
		t.Fatalf("rolled-back epoch 9: got %v, want ErrEpochRegression", err)
	}

	// A failed proof must not commit its epoch.
	bad := *proof
	bad.VRFProof = []byte("forged")
	if _, err := v.VerifyInclusion(100, root, &bad); err == nil {
		t.Fatal("forged VRF proof accepted")
	}
	if _, err := v.VerifyInclusion(11, root, proof); err != nil {
		t.Fatalf("epoch 11 rejected after failed epoch 100: %v", err)
	}

	// Non-inclusion proofs follow the same monotonic rule.
	_, niProof, niRoot := absentQuery(t, leaves, false)
	if err := v.VerifyNonInclusion(11, niRoot, niProof); !errors.Is(err, ErrEpochRegression) {
		t.Fatalf("non-inclusion at stale epoch 11: got %v, want ErrEpochRegression", err)
	}
	if err := v.VerifyNonInclusion(12, niRoot, niProof); err != nil {
		t.Fatalf("non-inclusion at epoch 12 rejected: %v", err)
	}

	epoch, ok := v.LastEpoch()
	if !ok || epoch != 12 {
		t.Fatalf("LastEpoch = %d, %v; want 12, true", epoch, ok)
	}
}

func TestVRFFailure(t *testing.T) {
	leaves := testTree()
	proof, root := inclusionProof(t, leaves, "alice")
	proof.VRFProof = []byte("forged")
	if _, err := VerifyInclusion(mockVRF{}, root, proof); err == nil {
		t.Fatal("invalid VRF proof accepted")
	}
}
