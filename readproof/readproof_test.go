package readproof

import (
	"bytes"
	"testing"
)

func TestLeafHashBindsAllFields(t *testing.T) {
	data := []byte("block-bytes")
	base := LeafHash(3, data)

	if LeafHash(4, data) == base {
		t.Fatal("leaf hash must bind block index")
	}
	extended := make([]byte, len(data)+1)
	copy(extended, data)
	if LeafHash(3, extended) == base {
		t.Fatal("leaf hash must bind effective length")
	}
	other := append([]byte(nil), data...)
	other[0] ^= 0xff
	if LeafHash(3, other) == base {
		t.Fatal("leaf hash must bind block bytes")
	}
}

func TestInnerHashBindsIntervalAndChildren(t *testing.T) {
	left := LeafHash(0, blockData(0))
	right := LeafHash(1, blockData(1))
	base := innerHash(0, 2, left, right)

	if innerHash(1, 3, left, right) == base {
		t.Fatal("inner hash must bind interval start")
	}
	if innerHash(0, 3, left, right) == base {
		t.Fatal("inner hash must bind interval end")
	}
	if innerHash(0, 2, right, left) == base {
		t.Fatal("inner hash must order child hashes")
	}
}

func TestSplitPointLargestPowerOfTwo(t *testing.T) {
	cases := map[uint64]uint64{
		2: 1, 3: 2, 4: 2, 5: 4, 7: 4, 8: 4, 9: 8, 15: 8, 16: 8, 17: 16,
	}
	for n, want := range cases {
		if got := splitPoint(n); got != want {
			t.Fatalf("splitPoint(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestEmptyFileStandaloneCommitment(t *testing.T) {
	tree := BuildTree(nil)
	if tree.Root() != EmptyRoot {
		t.Fatal("empty file must use the standalone empty commitment")
	}
	if NumLeaves(0) != 0 {
		t.Fatal("empty file has no leaves")
	}
}

func TestNonPowerOfTwoTreeStructure(t *testing.T) {
	for _, full := range []int{3, 5, 6, 7, 9, 13, 17} {
		data := fileData(full, 0)
		tree := BuildTree(data)
		if tree.Length() != uint64(len(data)) {
			t.Fatalf("length mismatch for %d blocks", full)
		}
		// Root node splits at the largest power of two below n.
		n := uint64(full)
		sp := splitPoint(n)
		if tree.root.left.start != 0 || tree.root.left.end != sp {
			t.Fatalf("n=%d left child interval wrong", n)
		}
		if tree.root.right.start != sp || tree.root.right.end != n {
			t.Fatalf("n=%d right child interval wrong", n)
		}
		if got := tree.findNode(0, sp).hash; got != tree.root.left.hash {
			t.Fatalf("n=%d left subtree lookup mismatch", n)
		}
	}
}

func TestValidLengthTailBlock(t *testing.T) {
	data := fileData(2, 17)
	length := uint64(len(data))
	if ValidLength(0, length) != BlockSize || ValidLength(1, length) != BlockSize {
		t.Fatal("non-tail blocks must be full sized")
	}
	if ValidLength(2, length) != 17 {
		t.Fatal("tail block must report its effective length")
	}
}

func TestVerifyMergedDisjointRanges(t *testing.T) {
	keys := testKeys(t)
	data := fileData(7, 33)
	tree := BuildTree(data)
	manifest := &Manifest{
		FileID:       "f",
		Length:       tree.Length(),
		Version:      1,
		PreviousRoot: EmptyRoot,
		Root:         tree.Root(),
	}
	if err := manifest.Sign(keys.sign); err != nil {
		t.Fatal(err)
	}

	ranges := []Range{{Start: 0, End: 1}, {Start: 3, End: 5}, {Start: 6, End: 7}}
	proof := mustProof(t, tree, ranges)
	blocks, err := Verify(manifest, keys.verify, proof)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(blocks) != 4 {
		t.Fatalf("got %d verified blocks, want 4", len(blocks))
	}
	for index, got := range blocks {
		off := index * BlockSize
		end := off + uint64(len(got))
		if !bytes.Equal(got, data[off:end]) {
			t.Fatalf("block %d content mismatch", index)
		}
	}
}

func TestVerifyRejectsMalformedProofs(t *testing.T) {
	keys := testKeys(t)
	data := fileData(6, 9)
	tree := BuildTree(data)
	manifest := &Manifest{
		FileID:       "f",
		Length:       tree.Length(),
		Version:      1,
		PreviousRoot: EmptyRoot,
		Root:         tree.Root(),
	}
	if err := manifest.Sign(keys.sign); err != nil {
		t.Fatal(err)
	}
	ranges := []Range{{Start: 1, End: 3}, {Start: 5, End: 6}}
	good := mustProof(t, tree, ranges)

	t.Run("duplicate leaf", func(t *testing.T) {
		bad := *good
		bad.Leaves = append(append([]Node(nil), good.Leaves...), good.Leaves[0])
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrUnexpectedLeaf {
			t.Fatalf("got %v, want ErrUnexpectedLeaf", err)
		}
	})

	t.Run("duplicate sibling", func(t *testing.T) {
		bad := *good
		bad.Nodes = append(append([]Node(nil), good.Nodes...), good.Nodes[0])
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrUnexpectedNode {
			t.Fatalf("got %v, want ErrUnexpectedNode", err)
		}
	})

	t.Run("extra range not in proof", func(t *testing.T) {
		bad := *good
		bad.Ranges = []Range{{Start: 1, End: 3}, {Start: 4, End: 5}, {Start: 5, End: 6}}
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrUnexpectedLeaf {
			t.Fatalf("got %v, want ErrUnexpectedLeaf", err)
		}
	})

	t.Run("extra sibling node", func(t *testing.T) {
		bad := *good
		extra := mustProof(t, tree, []Range{{Start: 0, End: 1}})
		bad.Nodes = append(append([]Node(nil), good.Nodes...), extra.Nodes...)
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrUnexpectedNode {
			t.Fatalf("got %v, want ErrUnexpectedNode", err)
		}
	})

	t.Run("wrong effective leaf length", func(t *testing.T) {
		bad := *good
		bad.Leaves = append([]Node(nil), good.Leaves...)
		bad.Leaves[0].Data = bad.Leaves[0].Data[:len(bad.Leaves[0].Data)-1]
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrBadLeafLength {
			t.Fatalf("got %v, want ErrBadLeafLength", err)
		}
	})

	t.Run("unordered ranges", func(t *testing.T) {
		bad := *good
		bad.Ranges = []Range{{Start: 5, End: 6}, {Start: 1, End: 3}}
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrRangeUnordered {
			t.Fatalf("got %v, want ErrRangeUnordered", err)
		}
	})

	t.Run("range out of bounds", func(t *testing.T) {
		bad := *good
		bad.Ranges = []Range{{Start: 7, End: 8}}
		if _, err := Verify(manifest, keys.verify, &bad); err != ErrRangeOutOfBounds {
			t.Fatalf("got %v, want ErrRangeOutOfBounds", err)
		}
	})

	t.Run("corrupted manifest signature", func(t *testing.T) {
		tampered := *manifest
		tampered.Length++
		if _, err := Verify(&tampered, keys.verify, good); err != ErrBadSignature {
			t.Fatalf("got %v, want ErrBadSignature", err)
		}
	})

	t.Run("old root rejected against new manifest", func(t *testing.T) {
		newData := fileData(6, 9)
		newData[0] ^= 0xff
		newTree := BuildTree(newData)
		newManifest := &Manifest{
			FileID:       "f",
			Length:       newTree.Length(),
			Version:      2,
			PreviousRoot: manifest.Root,
			Root:         newTree.Root(),
		}
		if err := newManifest.Sign(keys.sign); err != nil {
			t.Fatal(err)
		}
		// A correct sibling under the old root must not verify for the new handle.
		if _, err := Verify(newManifest, keys.verify, good); err != ErrProofMismatch {
			t.Fatalf("got %v, want ErrProofMismatch", err)
		}
	})
}
