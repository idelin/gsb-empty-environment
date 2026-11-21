package diffdisclose

import (
	"errors"
	"testing"
)

func TestTreeInputValidation(t *testing.T) {
	if _, err := NewTree(0, nil); !errors.Is(err, errBadHeight) {
		t.Fatalf("height 0: %v", err)
	}
	if _, err := NewTree(MaxHeight+1, nil); !errors.Is(err, errBadHeight) {
		t.Fatalf("oversized height: %v", err)
	}
	badKey := uint64(1) << 4
	if _, err := NewTree(4, []Leaf{{Key: badKey, Value: []byte("v"), Salt: saltFor(1)}}); !errors.Is(err, errKeyOutOfRange) {
		t.Fatalf("out-of-range key: %v", err)
	}
	if _, err := NewTree(4, []Leaf{{Key: 1, Value: []byte("v"), Salt: []byte("short")}}); !errors.Is(err, errBadSalt) {
		t.Fatalf("bad salt: %v", err)
	}
	good := Leaf{Key: 1, Value: []byte("v"), Salt: saltFor(1)}
	if _, err := NewTree(4, []Leaf{good, good}); !errors.Is(err, errDuplicateKey) {
		t.Fatalf("duplicate key: %v", err)
	}
}

func TestIntervalSetValidation(t *testing.T) {
	if _, err := NewIntervalSet([]Interval{{Lo: 5, Hi: 5}}); !errors.Is(err, errBadInterval) {
		t.Fatalf("empty interval: %v", err)
	}
	if _, err := NewIntervalSet([]Interval{{Lo: 9, Hi: 2}}); !errors.Is(err, errBadInterval) {
		t.Fatalf("inverted interval: %v", err)
	}
	// 重叠与相邻区间被归一化合并。
	set, err := NewIntervalSet([]Interval{{Lo: 0, Hi: 4}, {Lo: 4, Hi: 8}, {Lo: 2, Hi: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.items) != 1 || set.items[0] != (Interval{Lo: 0, Hi: 8}) {
		t.Fatalf("merge failed: %+v", set.items)
	}
	if !set.contains(7) || set.contains(8) {
		t.Fatalf("contains predicate wrong")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := DecodeProof(nil); !errors.Is(err, errMalformedProof) {
		t.Fatalf("nil proof: %v", err)
	}
	if _, err := DecodeProof([]byte("not-a-proof")); !errors.Is(err, errMalformedProof) {
		t.Fatalf("garbage: %v", err)
	}
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := proof.Encode()
	// 截断尾部必须失败而不是 panic。
	for _, cut := range []int{1, len(magic), len(encoded) - 1, len(encoded) / 2} {
		if _, err := DecodeProof(encoded[:cut]); !errors.Is(err, errMalformedProof) {
			t.Fatalf("truncation at %d accepted: %v", cut, err)
		}
	}
	// 追加多余字节必须失败。
	trailing := append(append([]byte(nil), encoded...), 0xFF)
	if _, err := DecodeProof(trailing); !errors.Is(err, errMalformedProof) {
		t.Fatalf("trailing bytes accepted: %v", err)
	}
}

func TestBuildProofRejectsRootMismatch(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	// 篡改新清单根但保留真实树，构造器必须拒绝。
	badNext := next
	badNext.Root = old.Root
	_, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: badNext,
		Authorized: fullRange(height),
	})
	if !errors.Is(err, errRootMismatch) {
		t.Fatalf("root mismatch accepted: %v", err)
	}
}

func TestVerifyRejectsWrongVerifier(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
	})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeProof(proof.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Verify(VerifyOptions{}); !errors.Is(err, errBadSignature) {
		t.Fatalf("missing verifier: %v", err)
	}
}
