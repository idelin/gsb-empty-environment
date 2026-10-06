package integrity

import (
	"bytes"
	"testing"
)

func TestBatchProofSameSnapshot(t *testing.T) {
	blocks := makeBlocks(16, "batch")
	blocks[4] = nil // a missing block inside the range
	tree := New(blocks)
	root := tree.Root()

	bp, proot, err := tree.ProveBatch([]uint64{9, 0, 4, 15})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(proot, root) {
		t.Fatal("proof root differs from tree root")
	}
	// Indices come back sorted; block 4 must be marked missing by nil.
	want := []uint64{0, 4, 9, 15}
	for i, idx := range bp.Indices {
		if idx != want[i] {
			t.Fatalf("indices = %v, want %v", bp.Indices, want)
		}
	}
	if bp.Blocks[1] != nil {
		t.Fatal("missing block 4 not represented as nil in batch proof")
	}
	if !VerifyBatchProof(root, bp) {
		t.Fatal("valid batch proof rejected")
	}
	// Each member must also verify as a single proof.
	for _, idx := range want {
		p, r, err := tree.Prove(idx)
		if err != nil || !VerifyProof(r, p) {
			t.Fatalf("single proof for %d failed: %v", idx, err)
		}
	}
	// Duplicate and out-of-range indices are rejected.
	if _, _, err := tree.ProveBatch([]uint64{1, 1}); err == nil {
		t.Fatal("duplicate indices accepted")
	}
	if _, _, err := tree.ProveBatch([]uint64{16}); err != ErrIndexOutOfRange {
		t.Fatalf("out-of-range batch err = %v", err)
	}
}

func TestBatchSharedPathNodeConsistency(t *testing.T) {
	blocks := makeBlocks(8, "shared")
	tree := New(blocks)
	root := tree.Root()

	// Indices 0 and 1 share their parent: the verifier recomputes it.
	bp, _, err := tree.ProveBatch([]uint64{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyBatchProof(root, bp) {
		t.Fatal("valid batch proof rejected")
	}
	// Inject a conflicting value for a node the verifier computes itself.
	conflict := bp
	conflict.Nodes = make(map[uint64][]byte, len(bp.Nodes)+1)
	for k, v := range bp.Nodes {
		conflict.Nodes[k] = v
	}
	bogus := clone(blocks[0])
	bogus[0] ^= 0xff
	conflict.Nodes[nodeID(1, 0)] = bogus // parent of leaves 0 and 1
	if VerifyBatchProof(root, conflict) {
		t.Fatal("conflicting shared path node accepted")
	}
	// Dropping a needed frontier node must fail.
	broken := bp
	broken.Nodes = make(map[uint64][]byte, len(bp.Nodes))
	for k, v := range bp.Nodes {
		broken.Nodes[k] = v
	}
	for k := range broken.Nodes {
		delete(broken.Nodes, k)
		break
	}
	if VerifyBatchProof(root, broken) {
		t.Fatal("batch proof with missing frontier node accepted")
	}
	// Tamper with a frontier node.
	for k, v := range bp.Nodes {
		bad := bp
		bad.Nodes = make(map[uint64][]byte, len(bp.Nodes))
		for k2, v2 := range bp.Nodes {
			bad.Nodes[k2] = v2
		}
		bad.Nodes[k] = clone(v)
		bad.Nodes[k][0] ^= 0x01
		if VerifyBatchProof(root, bad) {
			t.Fatalf("tampered frontier node %d accepted", k)
		}
	}
}

func TestBatchRejectsMaliciousMixedProof(t *testing.T) {
	blocksA := makeBlocks(8, "A")
	blocksB := makeBlocks(8, "A")
	// Concurrent modifications: block 5 is proven, block 4 sits on the
	// proof frontier, so splicing the two snapshots yields a root that
	// never existed.
	blocksB[5] = HashBlock([]byte("B-5"))
	blocksB[4] = HashBlock([]byte("B-4"))
	treeA := New(blocksA)
	treeB := New(blocksB)
	rootA := treeA.Root()
	rootB := treeB.Root()
	if bytes.Equal(rootA, rootB) {
		t.Fatal("test setup: roots must differ")
	}

	pa, _, err := treeA.ProveBatch([]uint64{2, 5, 7})
	if err != nil {
		t.Fatal(err)
	}
	pb, _, err := treeB.ProveBatch([]uint64{2, 5, 7})
	if err != nil {
		t.Fatal(err)
	}

	// Mix: block value from B, frontier nodes and root from A.
	mixed := pa
	mixed.Blocks = make([][]byte, len(pa.Blocks))
	copy(mixed.Blocks, pa.Blocks)
	mixed.Blocks[1] = pb.Blocks[1]
	if VerifyBatchProof(rootA, mixed) || VerifyBatchProof(rootB, mixed) {
		t.Fatal("mixed block values accepted against either root")
	}
	// Mix: frontier nodes from B spliced into A's proof.
	mixed = pa
	mixed.Nodes = make(map[uint64][]byte, len(pa.Nodes))
	for k, v := range pa.Nodes {
		mixed.Nodes[k] = v
	}
	for k, v := range pb.Nodes {
		mixed.Nodes[k] = v
	}
	if VerifyBatchProof(rootA, mixed) || VerifyBatchProof(rootB, mixed) {
		t.Fatal("mixed frontier nodes accepted against either root")
	}
	// A proof from one snapshot must never verify against the other root.
	if VerifyBatchProof(rootB, pa) || VerifyBatchProof(rootA, pb) {
		t.Fatal("proof verified against a foreign snapshot root")
	}
}

func TestUpdateProofBindsRootsAndCount(t *testing.T) {
	blocks := makeBlocks(8, "upd")
	tree := New(blocks)
	oldRoot := tree.Root()

	up, err := tree.Update(3, HashBlock([]byte("upd-3-new")))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(up.OldRoot, oldRoot) {
		t.Fatal("update proof old root mismatch")
	}
	if !bytes.Equal(up.NewRoot, tree.Root()) {
		t.Fatal("update proof new root mismatch")
	}
	if up.Total != 8 {
		t.Fatalf("update proof total = %d, want 8", up.Total)
	}
	if !VerifyUpdateProof(up) {
		t.Fatal("valid update proof rejected")
	}
	// The new root must match a full rebuild.
	blocks[3] = HashBlock([]byte("upd-3-new"))
	if !bytes.Equal(up.NewRoot, rebuildRoot(blocks)) {
		t.Fatal("update proof new root diverges from full rebuild")
	}

	// Updating a block back to missing also proves correctly.
	up2, err := tree.Update(3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if up2.NewBlock != nil || !VerifyUpdateProof(up2) {
		t.Fatal("update-to-missing proof failed")
	}

	// Tampering with any bound field must fail.
	bad := up
	bad.NewRoot = clone(up.NewRoot)
	bad.NewRoot[0] ^= 0x01
	if VerifyUpdateProof(bad) {
		t.Fatal("tampered new root accepted")
	}
	bad = up
	bad.OldRoot = clone(up.OldRoot)
	bad.OldRoot[0] ^= 0x01
	if VerifyUpdateProof(bad) {
		t.Fatal("tampered old root accepted")
	}
	bad = up
	bad.Total = 9
	if VerifyUpdateProof(bad) {
		t.Fatal("tampered block count accepted")
	}
	bad = up
	bad.Index = 4
	if VerifyUpdateProof(bad) {
		t.Fatal("rebound index accepted")
	}
	bad = up
	bad.NewBlock = HashBlock([]byte("something else"))
	if VerifyUpdateProof(bad) {
		t.Fatal("tampered new block accepted")
	}
}

func TestUpdateProofRejectsInconsistentPaths(t *testing.T) {
	blocksA := makeBlocks(8, "pathA")
	blocksB := makeBlocks(8, "pathB") // entirely different sibling nodes
	treeA := New(blocksA)
	treeB := New(blocksB)

	up, err := treeA.Update(2, HashBlock([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	// Splice the new path from an unrelated tree: shared path nodes no
	// longer agree between old and new.
	other, err := treeB.Update(2, HashBlock([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	spliced := up
	spliced.NewSiblings = other.NewSiblings
	spliced.NewRoot = other.NewRoot
	if VerifyUpdateProof(spliced) {
		t.Fatal("update proof with spliced foreign path accepted")
	}
	// Flip one shared sibling on only one side.
	bad := up
	bad.NewSiblings = make([][]byte, len(up.NewSiblings))
	copy(bad.NewSiblings, up.NewSiblings)
	bad.NewSiblings[0] = clone(up.NewSiblings[0])
	bad.NewSiblings[0][0] ^= 0x01
	if VerifyUpdateProof(bad) {
		t.Fatal("update proof with diverging shared nodes accepted")
	}
}
