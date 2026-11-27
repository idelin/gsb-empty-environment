package readproof

import (
	"bytes"
	"testing"
)

// publishFourVersions publishes one file identity across four consecutive
// versions:
//
//	v1: 6 full blocks + a 33-byte tail block (7 leaves),
//	v2: leaf 2 edited in place (same length, same leaf count),
//	v3: truncated to 5 full blocks (5 leaves; tail block removed),
//	v4: extended back to 7 leaves with new tail content.
func publishFourVersions(t *testing.T) (*Archive, keyPair, [4][]byte, [4]*Manifest) {
	t.Helper()
	keys := testKeys(t)
	archive := NewArchive("object/42", keys.sign)

	var data [4][]byte
	var manifests [4]*Manifest
	var err error

	data[0] = fileData(6, 33)
	manifests[0], err = archive.Publish(data[0], 1, EmptyRoot)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}

	data[1] = append([]byte(nil), data[0]...)
	copy(data[1][2*BlockSize:3*BlockSize], changedBlock(2))
	manifests[1], err = archive.Publish(data[1], 2, manifests[0].Root)
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}

	data[2] = fileData(5, 0)
	manifests[2], err = archive.Publish(data[2], 3, manifests[1].Root)
	if err != nil {
		t.Fatalf("publish v3: %v", err)
	}

	data[3] = append(fileData(6, 0), bytes.Repeat([]byte{0x7e}, 100)...)
	manifests[3], err = archive.Publish(data[3], 4, manifests[2].Root)
	if err != nil {
		t.Fatalf("publish v4: %v", err)
	}
	return archive, keys, data, manifests
}

func TestFourVersionHandlesKeepOldContent(t *testing.T) {
	archive, keys, data, manifests := publishFourVersions(t)
	cache := NewMemoryNodeCache()

	handles := make([]*Handle, 4)
	for i, manifest := range manifests {
		handle, err := archive.Open(manifest, keys.verify, cache)
		if err != nil {
			t.Fatalf("open v%d: %v", i+1, err)
		}
		handles[i] = handle
	}

	ranges := []Range{{Start: 0, End: 1}, {Start: 2, End: 3}, {Start: 6, End: 7}}
	expected := [][]byte{
		data[0][0:BlockSize],
		data[0][2*BlockSize : 3*BlockSize],
		data[0][6*BlockSize:],
	}

	// Read through the oldest handle after every newer snapshot exists.
	blocks, err := handles[0].Read(ranges)
	if err != nil {
		t.Fatalf("old handle read: %v", err)
	}
	for i, index := range []uint64{0, 2, 6} {
		if !bytes.Equal(blocks[index], expected[i]) {
			t.Fatalf("v1 handle returned wrong content for block %d", index)
		}
	}

	// Each handle must remain pinned to its own generation and content.
	for i, handle := range handles {
		got, err := handle.Read([]Range{{Start: 0, End: NumLeaves(manifests[i].Length)}})
		if err != nil {
			t.Fatalf("v%d full read: %v", i+1, err)
		}
		var reassembled []byte
		for index := uint64(0); index < NumLeaves(manifests[i].Length); index++ {
			reassembled = append(reassembled, got[index]...)
		}
		if !bytes.Equal(reassembled, data[i]) {
			t.Fatalf("v%d handle content drift", i+1)
		}
	}
}

func TestWrongGenerationCacheRejected(t *testing.T) {
	archive, keys, _, manifests := publishFourVersions(t)
	cache := NewMemoryNodeCache()

	v1, err := archive.Open(manifests[0], keys.verify, cache)
	if err != nil {
		t.Fatal(err)
	}
	// Populate the shared cache with v1 sibling commitments.
	proofRanges := []Range{{Start: 3, End: 4}}
	if _, err := v1.Read(proofRanges); err != nil {
		t.Fatal(err)
	}
	v1Data, err := archive.VersionData(manifests[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	v1Proof := mustProof(t, BuildTree(v1Data), proofRanges)

	v2, err := archive.Open(manifests[1], keys.verify, cache)
	if err != nil {
		t.Fatal(err)
	}
	// v1 and v2 share an identical tree shape, but the cache is root-scoped
	// so every v1 sibling is invisible when looked up under the v2 root.
	for _, node := range v1Proof.Nodes {
		if entry, found := cache.Get(manifests[1].Root, node.Start, node.End); found {
			t.Fatalf("wrong-generation cache entry visible: %+v", entry)
		}
	}
	if _, err := v2.Read(proofRanges); err != nil {
		t.Fatalf("v2 read must rebuild its own proof: %v", err)
	}
	for _, node := range v1Proof.Nodes {
		entry, found := cache.Get(manifests[1].Root, node.Start, node.End)
		if !found {
			t.Fatalf("v2 read must store entry for [%d,%d)", node.Start, node.End)
		}
		if entry.Version != manifests[1].Version {
			t.Fatalf("cached entry version = %d, want %d", entry.Version, manifests[1].Version)
		}
	}

	// Graft the correct v1 sibling into the structurally identical v2
	// proof: leaf 2 changed, so the shared subtree hashes differ and the
	// grafted proof must fail under the v2 root.
	v2Data, err := archive.VersionData(manifests[1].Version)
	if err != nil {
		t.Fatal(err)
	}
	v2Proof := mustProof(t, BuildTree(v2Data), proofRanges)
	graft := *v2Proof
	graft.Nodes = append([]Node(nil), v2Proof.Nodes...)
	grafted := false
	for i := range graft.Nodes {
		for _, oldNode := range v1Proof.Nodes {
			if oldNode.Start == graft.Nodes[i].Start &&
				oldNode.End == graft.Nodes[i].End &&
				oldNode.Hash != graft.Nodes[i].Hash {
				graft.Nodes[i].Hash = oldNode.Hash
				grafted = true
			}
		}
	}
	if !grafted {
		t.Fatal("test setup: no differing v1/v2 sibling available to graft")
	}
	if _, err := Verify(manifests[1], keys.verify, &graft); err != ErrProofMismatch {
		t.Fatalf("got %v, want ErrProofMismatch", err)
	}
}

func TestTailBlockLengthChange(t *testing.T) {
	archive, keys, _, manifests := publishFourVersions(t)

	if got := ValidLength(6, manifests[0].Length); got != 33 {
		t.Fatalf("v1 tail length = %d, want 33", got)
	}
	if got := ValidLength(6, manifests[3].Length); got != 100 {
		t.Fatalf("v4 tail length = %d, want 100", got)
	}

	handle, err := archive.Open(manifests[3], keys.verify, NewMemoryNodeCache())
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := handle.Read([]Range{{Start: 6, End: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks[6]) != 100 {
		t.Fatalf("v4 tail returned %d bytes", len(blocks[6]))
	}

	// A v1-style 33-byte tail must fail v1 length checks if transplanted.
	v4Data, err := archive.VersionData(manifests[3].Version)
	if err != nil {
		t.Fatal(err)
	}
	tree := BuildTree(v4Data)
	proof := mustProof(t, tree, []Range{{Start: 6, End: 7}})
	proof.Leaves[0].Data = proof.Leaves[0].Data[:33]
	if _, err := Verify(manifests[3], keys.verify, proof); err != ErrBadLeafLength {
		t.Fatalf("got %v, want ErrBadLeafLength", err)
	}
}
