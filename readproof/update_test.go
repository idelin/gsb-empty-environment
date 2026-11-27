package readproof

import "testing"

func changedLeavesFor(data []byte, indices ...uint64) []ChangedLeaf {
	out := make([]ChangedLeaf, 0, len(indices))
	for _, index := range indices {
		off := index * BlockSize
		end := off + BlockSize
		if end > uint64(len(data)) {
			end = uint64(len(data))
		}
		out = append(out, ChangedLeaf{Index: index, Data: data[off:end]})
	}
	return out
}

func TestIncrementalUpdateMatchesFullReference(t *testing.T) {
	_, keys, data, manifests := publishFourVersions(t)

	// v1 -> v2: leaf 2 edited, read a range that needs sibling subtrees.
	v1Tree := BuildTree(data[0])
	oldProof := mustProof(t, v1Tree, []Range{{Start: 0, End: 7}})
	updated, err := UpdateProof(
		manifests[0], manifests[1], keys.verify, oldProof,
		changedLeavesFor(data[1], 2),
		[]Range{{Start: 2, End: 3}, {Start: 5, End: 6}},
	)
	if err != nil {
		t.Fatalf("v1->v2 update: %v", err)
	}
	reference := mustProof(t, BuildTree(data[1]), []Range{{Start: 2, End: 3}, {Start: 5, End: 6}})
	assertSameProof(t, updated, reference)

	// v2 -> v3: truncation removes blocks 5 and 6. A read inside the
	// surviving region can still reuse unchanged full-block subtrees.
	oldProof = mustProof(t, BuildTree(data[1]), []Range{{Start: 0, End: 7}})
	updated, err = UpdateProof(
		manifests[1], manifests[2], keys.verify, oldProof,
		changedLeavesFor(data[2], 2),
		[]Range{{Start: 1, End: 4}},
	)
	if err != nil {
		t.Fatalf("v2->v3 update: %v", err)
	}
	reference = mustProof(t, BuildTree(data[2]), []Range{{Start: 1, End: 4}})
	assertSameProof(t, updated, reference)

	// v3 -> v4: extension adds leaves 5 and 6 back. The new leaves are
	// mandatory changed inputs.
	oldProof = mustProof(t, BuildTree(data[2]), []Range{{Start: 1, End: 5}})
	updated, err = UpdateProof(
		manifests[2], manifests[3], keys.verify, oldProof,
		changedLeavesFor(data[3], 5, 6),
		[]Range{{Start: 1, End: 7}},
	)
	if err != nil {
		t.Fatalf("v3->v4 update: %v", err)
	}
	reference = mustProof(t, BuildTree(data[3]), []Range{{Start: 1, End: 7}})
	assertSameProof(t, updated, reference)
}

func TestTruncatedTailCannotResurrect(t *testing.T) {
	_, keys, data, manifests := publishFourVersions(t)

	// The v2 proof knows the old tail block 6 (33 bytes). After v3
	// truncation and v4 re-extension, omitting block 6 from changed leaves
	// must fail instead of reusing the old tail.
	_, err := UpdateProof(
		manifests[2], manifests[3], keys.verify,
		mustProof(t, BuildTree(data[2]), []Range{{Start: 0, End: 5}}),
		changedLeavesFor(data[3], 5), // blocks 6 deliberately missing
		[]Range{{Start: 0, End: 7}},
	)
	if err != ErrMissingLeafData && err != ErrBadLeafLength {
		t.Fatalf("got %v, want ErrMissingLeafData or ErrBadLeafLength", err)
	}

	// Supplying the stale 33-byte v2 tail as a "changed" leaf must fail
	// length validation against v4's 100-byte tail.
	oldProof := mustProof(t, BuildTree(data[2]), []Range{{Start: 0, End: 5}})
	stale := ChangedLeaf{Index: 6, Data: data[0][6*BlockSize:]}
	if _, err := UpdateProof(
		manifests[2], manifests[3], keys.verify, oldProof,
		append(changedLeavesFor(data[3], 5, 6), stale),
		[]Range{{Start: 0, End: 7}},
	); err != ErrBadChangedLeaf {
		t.Fatalf("got %v, want ErrBadChangedLeaf", err)
	}
}

func TestUpdateRejectsUnchainedManifest(t *testing.T) {
	_, keys, data, manifests := publishFourVersions(t)
	oldProof := mustProof(t, BuildTree(data[0]), []Range{{Start: 0, End: 1}})

	skip := *manifests[2]
	skip.Version = 9
	if err := skip.Sign(keys.sign); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProof(manifests[0], &skip, keys.verify, oldProof, nil, []Range{{Start: 0, End: 1}}); err != ErrManifestChain {
		t.Fatalf("got %v, want ErrManifestChain", err)
	}

	other := *manifests[1]
	other.FileID = "object/99"
	if err := other.Sign(keys.sign); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProof(manifests[0], &other, keys.verify, oldProof, nil, []Range{{Start: 0, End: 1}}); err != ErrManifestChain {
		t.Fatalf("got %v, want ErrManifestChain", err)
	}
}

func TestIntersectingReadDeduplicatesProof(t *testing.T) {
	archive, keys, data, manifests := publishFourVersions(t)
	handle, err := archive.Open(manifests[3], keys.verify, NewMemoryNodeCache())
	if err != nil {
		t.Fatal(err)
	}

	intersecting := []Range{
		{Start: 0, End: 3},
		{Start: 2, End: 5}, // overlaps [0,3) at leaf 2
		{Start: 4, End: 7},
	}
	blocks, err := handle.Read(intersecting)
	if err != nil {
		t.Fatalf("intersecting read: %v", err)
	}
	if len(blocks) != 7 {
		t.Fatalf("got %d distinct blocks, want 7", len(blocks))
	}

	merged, err := MergeRanges(intersecting, NumLeaves(manifests[3].Length))
	if err != nil {
		t.Fatal(err)
	}
	want := []Range{{Start: 0, End: 7}}
	if len(merged) != 1 || merged[0] != want[0] {
		t.Fatalf("merged ranges = %v, want %v", merged, want)
	}

	// The verified merged proof must be identical to the full-file proof.
	tree := BuildTree(data[3])
	reference := mustProof(t, tree, want)
	proof := mustProof(t, tree, merged)
	assertSameProof(t, proof, reference)

	// A proof for a partial read rejects a manifest that claims an extra
	// range whose leaves the proof does not carry.
	partialRanges := []Range{{Start: 1, End: 3}, {Start: 4, End: 6}}
	partial := mustProof(t, tree, partialRanges)
	bad := *partial
	bad.Ranges = []Range{{Start: 1, End: 3}, {Start: 4, End: 7}}
	if _, err := Verify(manifests[3], keys.verify, &bad); err != ErrUnexpectedLeaf {
		t.Fatalf("got %v, want ErrUnexpectedLeaf", err)
	}
}

func TestEmptyFileLifecycle(t *testing.T) {
	keys := testKeys(t)
	archive := NewArchive("empty", keys.sign)
	manifest, err := archive.Publish(nil, 1, EmptyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Root != EmptyRoot {
		t.Fatal("empty manifest must bind EmptyRoot")
	}
	handle, err := archive.Open(manifest, keys.verify, NewMemoryNodeCache())
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := handle.Read(nil)
	if err != nil {
		t.Fatalf("empty read: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatal("empty file returns no blocks")
	}

	// Extend the empty file: every new block is a changed leaf.
	data := fileData(2, 5)
	next, err := archive.Publish(data, 2, manifest.Root)
	if err != nil {
		t.Fatal(err)
	}
	oldProof := &Proof{}
	updated, err := UpdateProof(
		manifest, next, keys.verify, oldProof,
		changedLeavesFor(data, 0, 1, 2),
		[]Range{{Start: 0, End: 3}},
	)
	if err != nil {
		t.Fatalf("empty->nonempty update: %v", err)
	}
	reference := mustProof(t, BuildTree(data), []Range{{Start: 0, End: 3}})
	assertSameProof(t, updated, reference)
}
