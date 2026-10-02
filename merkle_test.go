package erasable

import (
	"bytes"
	"testing"
)

func buildTree(t *testing.T, n int) (*Tree, [][]byte) {
	t.Helper()
	tr := NewTree()
	leaves := make([][]byte, n)
	for i := 0; i < n; i++ {
		leaves[i] = LeafHash([]byte{byte(i), byte(i >> 8), 0xAA})
		tr.Append(leaves[i])
	}
	return tr, leaves
}

func TestProofsVerifyForAllLeaves(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 16, 31} {
		tr, leaves := buildTree(t, n)
		root := tr.Root()
		for i := 0; i < n; i++ {
			p, err := tr.Prove(i)
			if err != nil {
				t.Fatalf("n=%d Prove(%d): %v", n, i, err)
			}
			if !p.Verify(leaves[i], root) {
				t.Fatalf("n=%d proof %d does not verify", n, i)
			}
		}
	}
}

func TestProofRejectsWrongLeafAndRoot(t *testing.T) {
	tr, leaves := buildTree(t, 6)
	root := tr.Root()
	p, _ := tr.Prove(2)
	if p.Verify(LeafHash([]byte("forged")), root) {
		t.Fatal("forged leaf must not verify")
	}
	other, _ := buildTree(t, 7)
	if p.Verify(leaves[2], other.Root()) {
		t.Fatal("proof must not verify against a different root")
	}
}

func TestRewriteLeafKeepsOtherProofsValid(t *testing.T) {
	tr, leaves := buildTree(t, 7)
	rootBefore := tr.Root()

	// Rewrite leaf 3 (simulating an Erase_Marker substitution).
	marker := LeafHash([]byte("some-marker"))
	if err := tr.SetLeaf(3, marker); err != nil {
		t.Fatal(err)
	}
	rootAfter := tr.Root()
	if bytes.Equal(rootBefore, rootAfter) {
		t.Fatal("root must evolve after leaf rewrite")
	}
	// Every untouched leaf still proves against the NEW root.
	for i := 0; i < 7; i++ {
		if i == 3 {
			continue
		}
		p, _ := tr.Prove(i)
		if !p.Verify(leaves[i], rootAfter) {
			t.Fatalf("proof for untouched leaf %d broke after rewrite", i)
		}
	}
	// The rewritten leaf proves with its new value, not the old one.
	p, _ := tr.Prove(3)
	if !p.Verify(marker, rootAfter) {
		t.Fatal("rewritten leaf must verify with new value")
	}
	if p.Verify(leaves[3], rootAfter) {
		t.Fatal("old leaf value must no longer verify")
	}
}

func TestIndexBounds(t *testing.T) {
	tr, _ := buildTree(t, 2)
	if _, err := tr.Prove(2); err != ErrIndexOutOfRange {
		t.Fatalf("expected ErrIndexOutOfRange, got %v", err)
	}
	if err := tr.SetLeaf(-1, nil); err != ErrIndexOutOfRange {
		t.Fatalf("expected ErrIndexOutOfRange, got %v", err)
	}
}
