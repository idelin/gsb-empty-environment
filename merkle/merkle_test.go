package merkle

import (
	"bytes"
	"fmt"
	"testing"
)

func buildAndVerify(t *testing.T, n int) {
	t.Helper()
	leaves := make([][]byte, n)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("leaf-%d", i))
	}
	tree := NewTree(leaves)
	root := tree.Root()
	if root == nil {
		t.Fatalf("n=%d: nil root", n)
	}
	for i := range leaves {
		p, err := tree.Prove(i)
		if err != nil {
			t.Fatalf("n=%d prove(%d): %v", n, i, err)
		}
		if p.Index != uint64(i) || p.Size != uint64(n) {
			t.Fatalf("n=%d prove(%d): bad metadata %+v", n, i, p)
		}
		if !VerifyProof(root, LeafHash(leaves[i]), p) {
			t.Fatalf("n=%d: proof for leaf %d failed verification", n, i)
		}
	}
}

func TestProveVerifyVariousSizes(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 100} {
		buildAndVerify(t, n)
	}
}

func TestProofRejectsTampering(t *testing.T) {
	leaves := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")}
	tree := NewTree(leaves)
	root := tree.Root()

	p, err := tree.Prove(1)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong leaf.
	if VerifyProof(root, LeafHash([]byte("x")), p) {
		t.Fatal("accepted wrong leaf")
	}
	// Wrong root.
	if VerifyProof(LeafHash([]byte("other-root")), LeafHash(leaves[1]), p) {
		t.Fatal("accepted wrong root")
	}
	// Flipped sibling side.
	bad := &Proof{Index: p.Index, Size: p.Size, Steps: make([]Step, len(p.Steps))}
	for i, s := range p.Steps {
		bad.Steps[i] = Step{Hash: s.Hash, Right: !s.Right}
	}
	if VerifyProof(root, LeafHash(leaves[1]), bad) {
		t.Fatal("accepted flipped sibling sides")
	}
	// Corrupted sibling hash.
	if len(p.Steps) > 0 {
		corrupt := &Proof{Index: p.Index, Size: p.Size, Steps: make([]Step, len(p.Steps))}
		copy(corrupt.Steps, p.Steps)
		h := bytes.Clone(p.Steps[0].Hash)
		h[0] ^= 0xff
		corrupt.Steps[0] = Step{Hash: h, Right: p.Steps[0].Right}
		if VerifyProof(root, LeafHash(leaves[1]), corrupt) {
			t.Fatal("accepted corrupted sibling hash")
		}
	}
}

func TestDynamicUpdateChangesRoot(t *testing.T) {
	leaves := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	tree := NewTree(leaves)
	r1 := tree.Root()

	tree.leaves[1] = []byte("b-updated")
	tree.Rebuild()
	r2 := tree.Root()
	if bytes.Equal(r1, r2) {
		t.Fatal("root did not change after leaf update")
	}
	p, err := tree.Prove(1)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(r2, LeafHash([]byte("b-updated")), p) {
		t.Fatal("proof for updated leaf failed")
	}
	if VerifyProof(r1, LeafHash([]byte("b-updated")), p) {
		t.Fatal("stale root still accepted")
	}
}

func TestEmptyAndBounds(t *testing.T) {
	tree := NewTree(nil)
	if tree.Root() != nil {
		t.Fatal("empty tree should have nil root")
	}
	if _, err := tree.Prove(0); err == nil {
		t.Fatal("expected error proving in empty tree")
	}
	tree = NewTree([][]byte{[]byte("only")})
	if _, err := tree.Prove(1); err == nil {
		t.Fatal("expected out-of-range error")
	}
	if _, err := tree.Prove(-1); err == nil {
		t.Fatal("expected negative-index error")
	}
}
