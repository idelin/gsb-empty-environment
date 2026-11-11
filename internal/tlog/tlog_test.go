package tlog

import (
	"bytes"
	"crypto/sha256"
	"math/bits"
	"testing"
)

func testLeaves(n int64) []Hash {
	leaves := make([]Hash, n)
	for i := range leaves {
		leaves[i] = RecordHash([]byte("record-" + itoa(int64(i))))
	}
	return leaves
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// recursiveRoot independently computes the root by splitting the leaves into
// the largest perfect subtree that fits on the left and the remaining leaves
// on the right, then recursing. It is deliberately a different algorithm from
// the popcount TreeHash it cross-checks.
func recursiveRoot(leaves []Hash) Hash {
	if len(leaves) == 0 {
		return Hash{}
	}

	if len(leaves) == 1 {
		return leaves[0]
	}

	// Largest power of two <= len(leaves).
	leftWidth := 1 << (bits.Len(uint(len(leaves))) - 1)
	if leftWidth == len(leaves) {
		return perfectRoot(leaves)
	}
	left := perfectRoot(leaves[:leftWidth])
	right := recursiveRoot(leaves[leftWidth:])
	return NodeHash(left, right)
}

// perfectRoot returns the root of a perfect power-of-two-sized leaf slice.
func perfectRoot(leaves []Hash) Hash {
	if len(leaves) == 1 {
		return leaves[0]
	}
	half := len(leaves) / 2
	return NodeHash(perfectRoot(leaves[:half]), perfectRoot(leaves[half:]))
}

func TestTreeHashMatchesRecursive(t *testing.T) {
	for n := int64(0); n <= 200; n++ {
		leaves := testLeaves(n)
		got := TreeHash(leaves)
		want := recursiveRoot(leaves)
		if got != want {
			t.Fatalf("TreeHash mismatch at n=%d", n)
		}
	}
}

func TestLeafAndNodeDomainSeparation(t *testing.T) {
	leaf := RecordHash([]byte("abc"))

	var manualLeaf Hash
	hl := sha256.New()
	hl.Write([]byte{0x00})
	hl.Write([]byte("abc"))
	hl.Sum(manualLeaf[:0])
	if leaf != manualLeaf {
		t.Fatal("leaf hash prefix mismatch")
	}

	a := RecordHash([]byte("a"))
	b := RecordHash([]byte("b"))
	node := NodeHash(a, b)
	var manualNode Hash
	hn := sha256.New()
	hn.Write([]byte{0x01})
	hn.Write(a[:])
	hn.Write(b[:])
	hn.Sum(manualNode[:0])
	if node != manualNode {
		t.Fatal("node hash prefix mismatch")
	}
	if node == leaf {
		t.Fatal("domain separation failed")
	}
}

// TestConsistencyProofExhaustive generates and verifies proofs for every
// (old, new) size pair up to a bound. This covers non-power-of-two sizes such
// as 3->6, 5->7 and 6->9 explicitly.
func TestConsistencyProofExhaustive(t *testing.T) {
	const max = 130
	leaves := testLeaves(max)

	for old := int64(1); old <= max; old++ {
		oldRoot := TreeHash(leaves[:old])
		for new := old; new <= max; new++ {
			newRoot := TreeHash(leaves[:new])

			proof, err := ConsistencyProof(leaves, old, new)
			if err != nil {
				t.Fatalf("proof gen old=%d new=%d: %v", old, new, err)
			}
			if err := CheckConsistency(old, new, oldRoot, newRoot, proof); err != nil {
				t.Fatalf("valid proof rejected old=%d new=%d len=%d: %v", old, new, len(proof), err)
			}

			// A proof for identical trees must be empty.
			if old == new && len(proof) != 0 {
				t.Fatalf("expected empty proof old=new=%d, got %d hashes", old, len(proof))
			}
		}
	}
}

func TestConsistencyRejectsTamperedProof(t *testing.T) {
	leaves := testLeaves(40)
	const old, new = int64(13), int64(37)
	oldRoot := TreeHash(leaves[:old])
	newRoot := TreeHash(leaves[:new])

	good, err := ConsistencyProof(leaves, old, new)
	if err != nil {
		t.Fatal(err)
	}
	if len(good) == 0 {
		t.Fatal("expected non-empty proof")
	}

	t.Run("flip every proof byte", func(t *testing.T) {
		for i := range good {
			for b := 0; b < len(good[i]); b++ {
				bad := cloneProof(good)
				bad[i][b] ^= 0xff
				if CheckConsistency(old, new, oldRoot, newRoot, bad) == nil {
					t.Fatalf("tampered proof accepted at hash=%d byte=%d", i, b)
				}
			}
		}
	})

	t.Run("truncated and extended", func(t *testing.T) {
		if CheckConsistency(old, new, oldRoot, newRoot, good[:len(good)-1]) == nil {
			t.Fatal("truncated proof accepted")
		}
		extra := append(cloneProof(good), good[0])
		if CheckConsistency(old, new, oldRoot, newRoot, extra) == nil {
			t.Fatal("extended proof accepted")
		}
		if CheckConsistency(old, new, oldRoot, newRoot, nil) == nil {
			t.Fatal("nil proof accepted")
		}
	})

	t.Run("wrong roots and sizes", func(t *testing.T) {
		var bogus Hash
		copy(bogus[:], []byte("not the real root, definitely 32 bytes"))
		if CheckConsistency(old, new, bogus, newRoot, good) == nil {
			t.Fatal("proof accepted with bogus old root")
		}
		if CheckConsistency(old, new, oldRoot, bogus, good) == nil {
			t.Fatal("proof accepted with bogus new root")
		}
		if CheckConsistency(new, old, newRoot, oldRoot, good) == nil {
			t.Fatal("reversed sizes accepted")
		}
		if err := CheckConsistency(old, new, oldRoot, newRoot, good); err != nil {
			t.Fatalf("sanity: original proof no longer verifies: %v", err)
		}
	})
}

func TestConsistencyRejectsForeignLog(t *testing.T) {
	leavesA := testLeaves(40)
	leavesB := make([]Hash, 40)
	for i := range leavesB {
		leavesB[i] = RecordHash([]byte("other-record-" + itoa(int64(i))))
	}
	const old, new = int64(7), int64(40)
	oldRoot := TreeHash(leavesA[:old])
	newRoot := TreeHash(leavesA[:new])

	// Proof built from a different log must never verify against A's roots.
	foreign, err := ConsistencyProof(leavesB, old, new)
	if err != nil {
		t.Fatal(err)
	}
	if CheckConsistency(old, new, oldRoot, newRoot, foreign) == nil {
		t.Fatal("foreign-log proof accepted")
	}

	// A same-size collision must be rejected.
	rootB := TreeHash(leavesB[:old])
	if CheckConsistency(old, old, oldRoot, rootB, nil) == nil {
		t.Fatal("different roots at identical size accepted")
	}
}

func TestConsistencyProofErrors(t *testing.T) {
	leaves := testLeaves(10)
	if _, err := ConsistencyProof(leaves, 5, 4); err == nil {
		t.Fatal("expected error for old>new")
	}
	if _, err := ConsistencyProof(leaves, 0, 11); err == nil {
		t.Fatal("expected error for oldSize=0 (no trusted root to bind)")
	}
	if err := CheckConsistency(0, 10, Hash{}, TreeHash(leaves), nil); err == nil {
		t.Fatal("expected CheckConsistency to reject oldSize=0")
	}
	if p, err := ConsistencyProof(leaves, 5, 5); err != nil || p != nil {
		t.Fatalf("old==new should yield empty proof, got %v %v", p, err)
	}
}

func TestHashIsBytes(t *testing.T) {
	h := RecordHash([]byte("x"))
	if len(h) != sha256.Size {
		t.Fatalf("hash size = %d", len(h))
	}
	if bytes.Equal(h[:], make([]byte, sha256.Size)) {
		t.Fatal("unexpected zero hash")
	}
}

func cloneProof(in []Hash) []Hash {
	out := make([]Hash, len(in))
	copy(out, in)
	return out
}
