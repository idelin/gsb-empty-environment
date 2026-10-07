package integrity

import (
	"bytes"
	"math/rand"
	"testing"
)

func randomBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	r.Read(b)
	return b
}

// randomBlocks builds total blocks, some missing (nil), some random, some
// all-zero, so zero-valued and missing blocks coexist.
func randomBlocks(r *rand.Rand, total int) [][]byte {
	blocks := make([][]byte, total)
	for i := range blocks {
		switch r.Intn(3) {
		case 0:
			blocks[i] = nil
		case 1:
			blocks[i] = make([]byte, 32) // present but all-zero
		default:
			blocks[i] = randomBytes(r, 1+r.Intn(64))
		}
	}
	return blocks
}

func TestProofRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, total := range []uint64{1, 2, 3, 5, 8, 9, 16, 17, 100} {
		blocks := randomBlocks(r, int(total))
		tree := NewTree(total, blocks)
		root := tree.Root()
		for i := uint64(0); i < total; i++ {
			p, err := tree.Prove(i)
			if err != nil {
				t.Fatalf("total=%d prove %d: %v", total, i, err)
			}
			if !VerifyProof(root, p) {
				t.Fatalf("total=%d proof %d does not verify", total, i)
			}
			if p.Present != (blocks[i] != nil) {
				t.Fatalf("total=%d proof %d presence mismatch", total, i)
			}
		}
	}
}

func TestEmptyFile(t *testing.T) {
	tree := NewTree(0, nil)
	root := tree.Root()
	if root != leafHashEmpty(0, 0) {
		t.Fatal("empty file root must be the explicit empty leaf")
	}
	if _, err := tree.Prove(0); err != ErrOutOfRange {
		t.Fatal("empty file must not prove any block")
	}
	// The empty-file root must not be replayable as a block proof.
	p := &Proof{Index: 0, Total: 1, Present: false, Siblings: nil}
	if VerifyProof(root, p) {
		t.Fatal("empty-file root accepted as a one-block proof")
	}
}

func TestLocalUpdateMatchesFullRebuild(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	const total = 37
	blocks := randomBlocks(r, total)
	tree := NewTree(total, blocks)
	for i := 0; i < 200; i++ {
		index := uint64(r.Intn(total))
		var data []byte
		switch r.Intn(3) {
		case 0:
			data = nil
		case 1:
			data = make([]byte, 16)
		default:
			data = randomBytes(r, 1+r.Intn(64))
		}
		if err := tree.Update(index, data); err != nil {
			t.Fatal(err)
		}
		blocks[index] = data
		rebuilt := NewTree(total, blocks)
		if tree.Root() != rebuilt.Root() {
			t.Fatalf("step %d: local update root != full rebuild root", i)
		}
	}
	if err := tree.Update(total, []byte("x")); err != ErrOutOfRange {
		t.Fatal("update beyond total must fail")
	}
}

func TestTamperedProofFails(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	blocks := randomBlocks(r, 11)
	tree := NewTree(11, blocks)
	root := tree.Root()
	p, err := tree.Prove(4)
	if err != nil {
		t.Fatal(err)
	}

	tampered := *p
	tampered.Siblings = append([]Hash(nil), p.Siblings...)
	tampered.Siblings[0][0] ^= 0xff
	if VerifyProof(root, &tampered) {
		t.Fatal("tampered sibling accepted")
	}

	tampered = *p
	tampered.Data = append([]byte(nil), p.Data...)
	if tampered.Present {
		tampered.Data[0] ^= 0xff
	} else {
		tampered.Present = true
		tampered.Data = []byte("forged")
	}
	if VerifyProof(root, &tampered) {
		t.Fatal("tampered data accepted")
	}

	tampered = *p
	tampered.Total = 12
	if VerifyProof(root, &tampered) {
		t.Fatal("tampered block count accepted")
	}

	tampered = *p
	tampered.Siblings = p.Siblings[:len(p.Siblings)-1]
	if VerifyProof(root, &tampered) {
		t.Fatal("truncated path accepted")
	}
}

func TestLeafCannotBeReplayedAtOtherPosition(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	blocks := randomBlocks(r, 10)
	tree := NewTree(10, blocks)
	root := tree.Root()
	src, err := tree.Prove(3)
	if err != nil {
		t.Fatal(err)
	}
	// Try to pass block 3's leaf off as block 7, keeping the honest
	// siblings of block 7 so only the leaf is foreign.
	dst, err := tree.Prove(7)
	if err != nil {
		t.Fatal(err)
	}
	forged := &Proof{
		Index:    7,
		Total:    src.Total,
		Present:  src.Present,
		Data:     src.Data,
		Siblings: dst.Siblings,
	}
	if VerifyProof(root, forged) {
		t.Fatal("leaf from another position accepted as block 7")
	}
	// Same attempt against a tree with a different block count must fail
	// even at the original position, because leaves commit to the count.
	other := NewTree(11, append(append([][]byte(nil), blocks...), nil))
	if VerifyProof(other.Root(), src) {
		t.Fatal("proof replayed against a tree with a different block count")
	}
}

func TestMissingLeafDistinctFromZeroLeaf(t *testing.T) {
	blocks := make([][]byte, 6)
	blocks[1] = make([]byte, 32) // present, all-zero
	// block 4 stays missing
	tree := NewTree(6, blocks)
	root := tree.Root()

	if leafHashEmpty(4, 6) == leafHashData(4, 6, make([]byte, 32)) {
		t.Fatal("empty leaf collides with zero-value leaf")
	}

	zero, err := tree.Prove(1)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := tree.Prove(4)
	if err != nil {
		t.Fatal(err)
	}
	if !zero.Present {
		t.Fatal("zero-value block must be proven as present")
	}
	if missing.Present {
		t.Fatal("missing block must be proven as absent")
	}
	if !VerifyProof(root, zero) || !VerifyProof(root, missing) {
		t.Fatal("honest proofs rejected")
	}

	// Claim the missing block actually holds zero data.
	forged := &Proof{Index: 4, Total: 6, Present: true, Data: make([]byte, 32), Siblings: missing.Siblings}
	if VerifyProof(root, forged) {
		t.Fatal("missing block forged as zero-value block")
	}
	// Claim the zero-value block is missing.
	forged = &Proof{Index: 1, Total: 6, Present: false, Siblings: zero.Siblings}
	if VerifyProof(root, forged) {
		t.Fatal("zero-value block forged as missing")
	}
}

func TestBatchProof(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	blocks := randomBlocks(r, 13)
	tree := NewTree(13, blocks)
	root := tree.Root()
	indices := []uint64{0, 1, 2, 3, 7, 12}
	bp, err := tree.ProveBatch(indices)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyBatchProof(root, bp) {
		t.Fatal("honest batch proof rejected")
	}

	// A conflicting duplicate of a shared path node must be rejected.
	conflicting := *bp
	conflicting.Nodes = append(append([]ProofNode(nil), bp.Nodes...), ProofNode{
		Level: bp.Nodes[0].Level,
		Index: bp.Nodes[0].Index,
		Hash:  leafHashEmpty(999, 13),
	})
	if VerifyBatchProof(root, &conflicting) {
		t.Fatal("conflicting shared node accepted")
	}

	// Dropping a needed node must be rejected.
	short := *bp
	short.Nodes = bp.Nodes[1:]
	if VerifyBatchProof(root, &short) {
		t.Fatal("incomplete batch proof accepted")
	}

	// Tampering with one proven block must be rejected.
	tampered := *bp
	tampered.Datas = append([][]byte(nil), bp.Datas...)
	tampered.Present = append([]bool(nil), bp.Present...)
	if tampered.Present[0] {
		tampered.Datas[0] = append([]byte(nil), bp.Datas[0]...)
		tampered.Datas[0][0] ^= 0xff
	} else {
		tampered.Present[0] = true
		tampered.Datas[0] = []byte("forged")
	}
	if VerifyBatchProof(root, &tampered) {
		t.Fatal("tampered batch member accepted")
	}

	// A batch valid for one snapshot must not verify against another root.
	other := NewTree(13, randomBlocks(r, 13))
	if VerifyBatchProof(other.Root(), bp) {
		t.Fatal("batch proof replayed against a foreign root")
	}
}

func TestUpdateProof(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	blocks := randomBlocks(r, 9)
	tree := NewTree(9, blocks)
	oldRoot := tree.Root()

	up, err := tree.ProveUpdate(5, []byte("new content"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.Update(5, []byte("new content")); err != nil {
		t.Fatal(err)
	}
	newRoot := tree.Root()

	if !VerifyUpdateProof(oldRoot, newRoot, up) {
		t.Fatal("honest update proof rejected")
	}
	if VerifyUpdateProof(newRoot, oldRoot, up) {
		t.Fatal("update proof accepted with swapped roots")
	}
	if VerifyUpdateProof(oldRoot, oldRoot, up) {
		t.Fatal("update proof accepted without root change")
	}

	// The proof binds the block count.
	tampered := *up
	tampered.Total = 10
	if VerifyUpdateProof(oldRoot, newRoot, &tampered) {
		t.Fatal("update proof with tampered block count accepted")
	}

	// The proof binds the new value.
	tampered = *up
	tampered.NewData = []byte("other content")
	if VerifyUpdateProof(oldRoot, newRoot, &tampered) {
		t.Fatal("update proof with tampered new value accepted")
	}

	// Removing a block (local update to missing) must also be provable.
	up2, err := tree.ProveUpdate(5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.Update(5, nil); err != nil {
		t.Fatal(err)
	}
	if !VerifyUpdateProof(newRoot, tree.Root(), up2) {
		t.Fatal("update-to-missing proof rejected")
	}
}

// TestConcurrentUpdatesCannotForgeRoot simulates two parties updating the
// same snapshot concurrently and an attacker splicing their proofs
// together. No combination of honest proof parts may verify against a root
// that never existed, and honest sequential updates must match a full
// rebuild.
func TestConcurrentUpdatesCannotForgeRoot(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	blocks := randomBlocks(r, 16)
	base := NewTree(16, blocks)
	root0 := base.Root()

	// Two concurrent local updates against the same snapshot.
	upA, err := base.ProveUpdate(2, []byte("update A"))
	if err != nil {
		t.Fatal(err)
	}
	upB, err := base.ProveUpdate(9, []byte("update B"))
	if err != nil {
		t.Fatal(err)
	}

	// Each update applied alone yields a root that honestly existed.
	treeA := NewTree(16, blocks)
	if err := treeA.Update(2, []byte("update A")); err != nil {
		t.Fatal(err)
	}
	rootA := treeA.Root()
	treeB := NewTree(16, blocks)
	if err := treeB.Update(9, []byte("update B")); err != nil {
		t.Fatal(err)
	}
	rootB := treeB.Root()

	if !VerifyUpdateProof(root0, rootA, upA) || !VerifyUpdateProof(root0, rootB, upB) {
		t.Fatal("honest concurrent update proofs rejected")
	}

	// Applying both updates (in either order) must match a full rebuild.
	seq := NewTree(16, blocks)
	if err := seq.Update(2, []byte("update A")); err != nil {
		t.Fatal(err)
	}
	if err := seq.Update(9, []byte("update B")); err != nil {
		t.Fatal(err)
	}
	blocksAB := append([][]byte(nil), blocks...)
	blocksAB[2] = []byte("update A")
	blocksAB[9] = []byte("update B")
	if seq.Root() != NewTree(16, blocksAB).Root() {
		t.Fatal("sequential updates diverge from full rebuild")
	}
	rootAB := seq.Root()

	// Malicious mixing 1: new leaf from A's proof, sibling path from B's
	// proof. It must not verify against any root, real or claimed.
	mixed := &UpdateProof{
		Index:      upA.Index,
		Total:      upA.Total,
		OldPresent: upA.OldPresent,
		OldData:    upA.OldData,
		NewPresent: upA.NewPresent,
		NewData:    upA.NewData,
		Siblings:   upB.Siblings,
	}
	for _, claimed := range []Hash{root0, rootA, rootB, rootAB, leafHashEmpty(0, 16)} {
		if VerifyUpdateProof(root0, claimed, mixed) {
			t.Fatal("mixed proof verified against a claimed root")
		}
	}

	// Malicious mixing 2: splice B's new value into A's update and claim
	// the combined root rootAB, which the attacker has observed.
	spliced := *upA
	spliced.NewData = upB.NewData
	if VerifyUpdateProof(root0, rootAB, &spliced) {
		t.Fatal("spliced proof verified against the combined root")
	}

	// Malicious mixing 3: replay A's proof to "re-apply" it on top of
	// rootB, claiming a root that never existed.
	if VerifyUpdateProof(rootB, rootAB, upA) {
		t.Fatal("stale proof replayed onto a foreign snapshot")
	}

	// A never-existing root cannot be certified by any honest proof part.
	neverRoot := leafHashData(0, 16, []byte("never existed"))
	if VerifyUpdateProof(root0, neverRoot, upA) || VerifyUpdateProof(root0, neverRoot, upB) {
		t.Fatal("honest proof part certified a never-existing root")
	}
}

func TestProofsAfterLocalUpdates(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	blocks := randomBlocks(r, 21)
	tree := NewTree(21, blocks)
	// Interleave local updates with proof checks against the current root.
	for step := 0; step < 50; step++ {
		index := uint64(r.Intn(21))
		data := randomBytes(r, 8)
		up, err := tree.ProveUpdate(index, data)
		if err != nil {
			t.Fatal(err)
		}
		oldRoot := tree.Root()
		if err := tree.Update(index, data); err != nil {
			t.Fatal(err)
		}
		blocks[index] = data
		if !VerifyUpdateProof(oldRoot, tree.Root(), up) {
			t.Fatalf("step %d: update proof rejected", step)
		}
		if tree.Root() != NewTree(21, blocks).Root() {
			t.Fatalf("step %d: root diverged from rebuild", step)
		}
		p, err := tree.Prove(index)
		if err != nil {
			t.Fatal(err)
		}
		if !VerifyProof(tree.Root(), p) || !bytes.Equal(p.Data, data) {
			t.Fatalf("step %d: fresh proof rejected", step)
		}
	}
}
