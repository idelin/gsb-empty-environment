package diffdisclose

import (
	"bytes"
	"errors"
	"testing"
)

func TestEmptyDigestsAreHeightBound(t *testing.T) {
	e0, err := EmptyDigest(0)
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := EmptyDigest(1)
	e48, _ := EmptyDigest(48)
	if e0 == e1 || e1 == e48 || e0 == e48 {
		t.Fatalf("empty digests must be distinct per height")
	}
	if _, err := EmptyDigest(-1); err == nil {
		t.Fatalf("negative height must fail")
	}
	if _, err := EmptyDigest(MaxHeight + 1); err == nil {
		t.Fatalf("oversized height must fail")
	}
}

func TestLeafAndInternalDomainSeparation(t *testing.T) {
	salt := saltFor(1)
	leaf := leafDigest(7, []byte("v"), salt)
	other := leafDigest(7, []byte("w"), salt)
	if leaf == other {
		t.Fatalf("value change must change leaf digest")
	}
	left := mustEmptyDigest(1)
	right := leaf
	a := internalDigest(2, rootAddr(), left, right)
	b := internalDigest(2, rootAddr().right(), left, right)
	if a == b {
		t.Fatalf("internal digest must bind prefix")
	}
	c := internalDigest(3, rootAddr(), left, right)
	if a == c {
		t.Fatalf("internal digest must bind height")
	}
	if leaf == a {
		t.Fatalf("domain separation failed between leaf and internal")
	}
}

func TestTreeRootAndLeafDigest(t *testing.T) {
	tree := treeFromMap(t, 4, map[uint64]uint64{1: 10, 2: 20, 9: 30})
	if tree.Root() == mustEmptyDigest(4) {
		t.Fatalf("non-empty tree cannot share the empty root")
	}
	// 相同输入必须得到相同根。
	tree2 := treeFromMap(t, 4, map[uint64]uint64{1: 10, 2: 20, 9: 30})
	if tree.Root() != tree2.Root() {
		t.Fatalf("deterministic root mismatch")
	}
}

func TestBasicDiffEventsAndNoRepeatDisclosure(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{
		1: 100, // 保持不变
		2: 200, // 删除
		3: 300, // 改变
	})
	newTree := treeFromMap(t, height, map[uint64]uint64{
		1: 100, // 保持不变
		3: 399, // 改变
		4: 400, // 新增
	})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, result, stats := env.buildAndVerify(t, BuildRequest{
		OldTree:    oldTree,
		NewTree:    newTree,
		Old:        old,
		New:        next,
		Authorized: fullRange(height),
		Revoked:    IntervalSet{},
	})

	got := map[uint64]EventKind{}
	for _, ev := range result.Events {
		if _, dup := got[ev.Key]; dup {
			t.Fatalf("duplicate event for key %d", ev.Key)
		}
		got[ev.Key] = ev.Kind
	}
	want := map[uint64]EventKind{2: EventRemoved, 3: EventChanged, 4: EventAdded}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for key, kind := range want {
		if got[key] != kind {
			t.Fatalf("key %d kind = %v, want %v", key, got[key], kind)
		}
	}

	// 未变化键 1 不得产生任何差分事件；即便因与变更键共享祖先而携带
	// 双侧载荷，两侧值也必须一致且只来自同一份未变化内容。
	for _, rec := range proof.Records {
		if rec.kind == recLeafUpdate && rec.key == 1 {
			if rec.oldLeaf == nil || rec.newLeaf == nil ||
				!bytes.Equal(rec.oldLeaf.Value, rec.newLeaf.Value) ||
				!bytes.Equal(rec.oldLeaf.Salt, rec.newLeaf.Salt) {
				t.Fatalf("unchanged key payloads must be identical")
			}
		}
	}
	if stats.LeafUpdates != 3 {
		t.Fatalf("LeafUpdates = %d, want 3", stats.LeafUpdates)
	}
	// 整棵树存在多处相等同址子树共用记录，证明未变化内容被省略。
	if stats.SharedInternals == 0 {
		t.Fatalf("equal same-address subtrees must be shared")
	}
}

func TestGenesisAndEmptyTree(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree, _ := NewTree(height, nil)
	newTree := treeFromMap(t, height, map[uint64]uint64{0: 1, 5: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	_, result, _ := env.buildAndVerify(t, BuildRequest{
		OldTree:    oldTree,
		NewTree:    newTree,
		Old:        old,
		New:        next,
		Authorized: fullRange(height),
	})
	kinds := map[uint64]EventKind{}
	for _, ev := range result.Events {
		kinds[ev.Key] = ev.Kind
	}
	if kinds[0] != EventAdded || kinds[5] != EventAdded || len(kinds) != 2 {
		t.Fatalf("genesis adds = %v", kinds)
	}
	if result.OldRoot != mustEmptyDigest(height) {
		t.Fatalf("genesis old root must be the precomputed empty root")
	}
}

func TestCanonicalEncodingIsStable(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1, 6: 2})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 1, 6: 9, 7: 3})
	old, next := env.manifests(t, oldTree, newTree, 2)
	auth := mustSet(t, Interval{Lo: 0, Hi: 8}, Interval{Lo: 8, Hi: 16})
	req := BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: auth,
	}
	p1, _, err := BuildProof(req)
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := BuildProof(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p1.Encode(), p2.Encode()) {
		t.Fatalf("canonical encoding must be deterministic")
	}
}

func TestRejectBadSignatureAndParentLink(t *testing.T) {
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
	tampered := *proof
	tampered.NewManifest = next
	tampered.NewManifest.Root = old.Root
	encoded := tampered.Encode()
	dec, err := DecodeProof(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Verify(VerifyOptions{Verifier: env.verifier}); !errors.Is(err, errBadSignature) {
		t.Fatalf("tampered root, got err %v", err)
	}

	// 父清单链接被破坏（连同其签名）也必须被拒绝，不得通过验证。
	dec2, _ := DecodeProof(proof.Encode())
	dec2.NewManifest.Parent = Digest{}
	if _, err := dec2.Verify(VerifyOptions{Verifier: env.verifier}); err == nil {
		t.Fatalf("broken parent link must be rejected")
	}
}
