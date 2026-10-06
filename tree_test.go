package integrity

import (
	"bytes"
	"fmt"
	"testing"
)

func makeBlocks(n int, prefix string) [][]byte {
	blocks := make([][]byte, n)
	for i := range blocks {
		blocks[i] = HashBlock([]byte(fmt.Sprintf("%s-%d", prefix, i)))
	}
	return blocks
}

// rebuildRoot reconstructs the whole tree from scratch, used to check
// that incremental updates never diverge from a full rebuild.
func rebuildRoot(blocks [][]byte) []byte {
	return New(blocks).Root()
}

func TestEmptyFile(t *testing.T) {
	tree := New(nil)
	if tree.Total() != 0 {
		t.Fatalf("empty tree total = %d, want 0", tree.Total())
	}
	root := tree.Root()
	if len(root) != HashSize {
		t.Fatalf("empty tree root size = %d", len(root))
	}
	if !bytes.Equal(root, New([][]byte{}).Root()) {
		t.Fatal("empty tree root not deterministic")
	}
	if _, _, err := tree.Prove(0); err != ErrIndexOutOfRange {
		t.Fatalf("Prove on empty file err = %v", err)
	}
	if _, err := tree.Update(0, HashBlock([]byte("x"))); err != ErrIndexOutOfRange {
		t.Fatalf("Update on empty file err = %v", err)
	}
	// An empty file can grow; new blocks start out missing.
	tree.Resize(3)
	if tree.Total() != 3 {
		t.Fatalf("total after resize = %d", tree.Total())
	}
	for i := uint64(0); i < 3; i++ {
		p, root, err := tree.Prove(i)
		if err != nil {
			t.Fatalf("Prove(%d): %v", i, err)
		}
		if !p.Missing {
			t.Fatalf("block %d should be missing after grow", i)
		}
		if !VerifyProof(root, p) {
			t.Fatalf("missing-leaf proof %d does not verify", i)
		}
	}
}

func TestRootMatchesFullRebuild(t *testing.T) {
	blocks := makeBlocks(13, "blk") // 13 pads to 16
	tree := New(blocks)
	if !bytes.Equal(tree.Root(), rebuildRoot(blocks)) {
		t.Fatal("initial root mismatch")
	}
	// Local updates, including setting a block missing again.
	updates := []struct {
		index uint64
		hash  []byte
	}{
		{0, HashBlock([]byte("new-0"))},
		{12, HashBlock([]byte("new-12"))},
		{5, nil},
		{7, HashBlock([]byte("new-7"))},
	}
	for _, u := range updates {
		if _, err := tree.Update(u.index, u.hash); err != nil {
			t.Fatalf("Update(%d): %v", u.index, err)
		}
		blocks[u.index] = u.hash
		if !bytes.Equal(tree.Root(), rebuildRoot(blocks)) {
			t.Fatalf("root after Update(%d) diverges from full rebuild", u.index)
		}
	}
	// Growth and truncation also match a full rebuild.
	tree.Resize(20)
	blocks = append(blocks, make([][]byte, 7)...)
	if !bytes.Equal(tree.Root(), rebuildRoot(blocks)) {
		t.Fatal("root after grow diverges from full rebuild")
	}
	tree.Resize(4)
	if !bytes.Equal(tree.Root(), rebuildRoot(blocks[:4])) {
		t.Fatal("root after truncate diverges from full rebuild")
	}
}

func TestMissingLeafDistinctFromZeroValueBlock(t *testing.T) {
	zeroBlock := HashBlock(make([]byte, 64)) // present block of zero bytes
	blocks := [][]byte{zeroBlock, nil}
	tree := New(blocks)
	root := tree.Root()

	p0, _, err := tree.Prove(0)
	if err != nil {
		t.Fatal(err)
	}
	p1, _, err := tree.Prove(1)
	if err != nil {
		t.Fatal(err)
	}
	if p0.Missing || !p1.Missing {
		t.Fatal("missing flags wrong")
	}
	if !VerifyProof(root, p0) || !VerifyProof(root, p1) {
		t.Fatal("valid proofs rejected")
	}
	// A zero-value block must not verify as a missing leaf and vice versa.
	forged := p1
	forged.Missing = false
	forged.BlockHash = zeroBlock
	if VerifyProof(root, forged) {
		t.Fatal("zero-value block accepted as missing leaf")
	}
	forged = p0
	forged.Missing = true
	forged.BlockHash = nil
	if VerifyProof(root, forged) {
		t.Fatal("missing leaf accepted for present zero-value block")
	}
}

func TestProofBindsIndexAndTotal(t *testing.T) {
	blocks := makeBlocks(8, "bind")
	tree := New(blocks)
	root := tree.Root()

	p, _, err := tree.Prove(3)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(root, p) {
		t.Fatal("valid proof rejected")
	}
	// Rebinding the same leaf to another position must fail.
	moved := p
	moved.Index = 4
	if VerifyProof(root, moved) {
		t.Fatal("leaf from position 3 accepted at position 4")
	}
	// Same blocks under a different block count must fail too.
	other := p
	other.Total = 9
	if VerifyProof(root, other) {
		t.Fatal("proof accepted under a different block count")
	}
	// And against the root of a resized snapshot.
	tree.Resize(9)
	if VerifyProof(tree.Root(), p) {
		t.Fatal("proof from 8-block snapshot accepted for 9-block root")
	}
}

func TestTamperedProofNodesRejected(t *testing.T) {
	blocks := makeBlocks(10, "tamper")
	tree := New(blocks)
	root := tree.Root()

	p, _, err := tree.Prove(6)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(root, p) {
		t.Fatal("valid proof rejected")
	}
	// Flip one bit in every sibling in turn.
	for level := range p.Siblings {
		bad := p
		bad.Siblings = make([][]byte, len(p.Siblings))
		copy(bad.Siblings, p.Siblings)
		bad.Siblings[level] = clone(p.Siblings[level])
		bad.Siblings[level][0] ^= 0x01
		if VerifyProof(root, bad) {
			t.Fatalf("proof with tampered sibling at level %d accepted", level)
		}
	}
	// Tamper with the block itself.
	bad := p
	bad.BlockHash = clone(p.BlockHash)
	bad.BlockHash[HashSize-1] ^= 0x80
	if VerifyProof(root, bad) {
		t.Fatal("proof with tampered block hash accepted")
	}
	// Wrong root must fail.
	if VerifyProof(rebuildRoot(makeBlocks(10, "other")), p) {
		t.Fatal("proof accepted against foreign root")
	}
}
