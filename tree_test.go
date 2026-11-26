package kt

import "testing"

func TestFullyExpandedReferenceTreeAndInclusion(t *testing.T) {
	l1 := mustPrefix(t, "001011")
	l2 := mustPrefix(t, "110100")
	l3 := mustPrefix(t, "110101")
	tr := referenceTree(t, 2,
		Entry{l1, leaf("alice", false, 0)},
		Entry{l2, leaf("bob", false, 0)},
		Entry{l3, leaf("carol", true, 1)},
	)
	if tr.LabelDepth() != testDepth {
		t.Fatalf("depth = %d", tr.LabelDepth())
	}
	// Root determinism: rebuild and compare.
	tr2 := referenceTree(t, 2,
		Entry{l1, leaf("alice", false, 0)},
		Entry{l2, leaf("bob", false, 0)},
		Entry{l3, leaf("carol", true, 1)},
	)
	if tr.RootHash() != tr2.RootHash() {
		t.Fatal("nondeterministic root")
	}
	// Epoch binding: same entries, different epoch must differ.
	tr3 := referenceTree(t, 3,
		Entry{l1, leaf("alice", false, 0)},
		Entry{l2, leaf("bob", false, 0)},
		Entry{l3, leaf("carol", true, 1)},
	)
	if tr3.RootHash() == tr.RootHash() {
		t.Fatal("epoch number not bound into root")
	}

	for _, label := range []BitPrefix{l1, l2, l3} {
		proof, err := tr.ProveInclusion(label)
		if err != nil {
			t.Fatal(err)
		}
		if err := proof.Verify(); err != nil {
			t.Fatalf("inclusion verify: %v", err)
		}
		if len(proof.Steps) != testDepth {
			t.Fatalf("steps = %d", len(proof.Steps))
		}
	}
	// Non-inclusion for an empty label.
	empty := mustPrefix(t, "000001")
	np, err := tr.ProveInclusion(empty)
	if err != nil {
		t.Fatal(err)
	}
	if np.Present || np.Verify() != nil {
		t.Fatal("non-inclusion proof invalid")
	}

	// Tamper with a sibling hash: verification must fail.
	proof, _ := tr.ProveInclusion(l1)
	proof.Steps[0].SiblingHash[0] ^= 0xFF
	if err := proof.Verify(); err != ErrRootMismatch {
		t.Fatalf("tamper not detected: %v", err)
	}
}

func TestTreeRejectsBadDepthAndDuplicates(t *testing.T) {
	good := mustPrefix(t, "000000")
	bad := mustPrefix(t, "00000")
	if _, err := BuildTree(1, testDepth, []Entry{{bad, leaf("x", false, 0)}}); err != ErrLabelDepth {
		t.Fatalf("got %v", err)
	}
	if _, err := BuildTree(1, testDepth, []Entry{
		{good, leaf("x", false, 0)},
		{good, leaf("y", false, 0)},
	}); err != ErrDuplicateLabel {
		t.Fatalf("got %v", err)
	}
}

func TestDomainTagsAreDistinct(t *testing.T) {
	p := mustPrefix(t, "010111")
	h1 := LeafHash(1, p, sum256([]byte("v")))
	h2 := InteriorHash(1, 1, p, h1, h1)
	h3 := EmptyHash(1, 1, p)
	if h1 == h2 || h2 == h3 || h1 == h3 {
		t.Fatal("domain tag collision across node roles")
	}
	// Height and prefix binding.
	h4 := InteriorHash(1, 2, p, h1, h1)
	h5 := InteriorHash(2, 1, p, h1, h1)
	if h4 == h2 || h5 == h2 {
		t.Fatal("height/epoch not bound into interior hash")
	}
}
