package readproof

import "testing"

// TestProofsVerifyForAllLeafCounts exercises non-power-of-two tree shapes
// and every single-block read, asserting the full-tree reference root and
// the strict verifier agree for each.
func TestProofsVerifyForAllLeafCounts(t *testing.T) {
	keys := testKeys(t)
	for leaves := uint64(1); leaves <= 33; leaves++ {
		data := fileData(int(leaves)-1, 17) // full blocks + one partial tail
		tree := BuildTree(data)
		if NumLeaves(tree.Length()) != leaves {
			t.Fatalf("leaf count mismatch for n=%d", leaves)
		}
		manifest := &Manifest{
			FileID:       "prop",
			Length:       tree.Length(),
			Version:      1,
			PreviousRoot: EmptyRoot,
			Root:         tree.Root(),
		}
		if err := manifest.Sign(keys.sign); err != nil {
			t.Fatal(err)
		}
		for index := uint64(0); index < leaves; index++ {
			proof := mustProof(t, tree, []Range{{Start: index, End: index + 1}})
			blocks, err := Verify(manifest, keys.verify, proof)
			if err != nil {
				t.Fatalf("n=%d leaf=%d verify: %v", leaves, index, err)
			}
			off := index * BlockSize
			want := data[off:]
			if uint64(len(want)) > BlockSize {
				want = data[off : off+BlockSize]
			}
			if got := blocks[index]; string(got) != string(want) {
				t.Fatalf("n=%d leaf=%d content mismatch", leaves, index)
			}
		}
	}
}

// TestMultiRangeProofsCoverUnion verifies merged proofs over several
// disjoint ranges return exactly the union of requested blocks.
func TestMultiRangeProofsCoverUnion(t *testing.T) {
	keys := testKeys(t)
	data := fileData(11, 400)
	tree := BuildTree(data)
	manifest := &Manifest{
		FileID:       "union",
		Length:       tree.Length(),
		Version:      1,
		PreviousRoot: EmptyRoot,
		Root:         tree.Root(),
	}
	if err := manifest.Sign(keys.sign); err != nil {
		t.Fatal(err)
	}
	ranges := []Range{{Start: 0, End: 2}, {Start: 4, End: 6}, {Start: 9, End: 12}}
	proof := mustProof(t, tree, ranges)
	blocks, err := Verify(manifest, keys.verify, proof)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint64]bool{0: true, 1: true, 4: true, 5: true, 9: true, 10: true, 11: true}
	if len(blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d", len(blocks), len(want))
	}
	for index := range want {
		if _, ok := blocks[index]; !ok {
			t.Fatalf("missing verified block %d", index)
		}
	}

	// Removing any required sibling breaks root verification.
	if len(proof.Nodes) == 0 {
		t.Fatal("expected sibling nodes for a sparse multi-range read")
	}
	stripped := *proof
	stripped.Nodes = append([]Node(nil), proof.Nodes[:len(proof.Nodes)-1]...)
	if _, err := Verify(manifest, keys.verify, &stripped); err == nil {
		t.Fatal("proof with a missing sibling must fail")
	}
}
