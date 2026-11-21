package diffdisclose

import "testing"

// TestLeafLevelRevocation 撤权区间可以窄到单个键（高度 0 对齐），
// 该叶子只能以盲终端出现，不得披露值或盐。
func TestLeafLevelRevocation(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{2: 1, 3: 2})
	newTree := treeFromMap(t, height, map[uint64]uint64{2: 1, 3: 99})
	old, next := env.manifests(t, oldTree, newTree, 1)
	revoked := mustSet(t, Interval{Lo: 3, Hi: 4})
	proof, result, stats, err := buildWithStats(env, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
		Revoked:    revoked,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range result.Events {
		if ev.Key == 3 {
			t.Fatalf("revoked leaf key 3 must not produce an event")
		}
	}
	for _, rec := range proof.Records {
		if rec.kind == recBlindRevoked {
			if rec.blindOld == rec.blindNew {
				// 两侧内容不同，盲摘要理应不同；若相同则测试构造有误。
				t.Fatalf("test setup: revoked leaf changed but digests equal")
			}
		}
		if rec.key == 3 && (rec.oldLeaf != nil || rec.newLeaf != nil) {
			t.Fatalf("revoked leaf payload disclosed")
		}
	}
	if stats.BlindRevoked == 0 {
		t.Fatalf("leaf-level revocation must emit a blind terminal")
	}
}

// TestMultipleRevokedSubtrees 多个撤权子区间各自产生盲终端，
// 且其余授权区差分仍完整。
func TestMultipleRevokedSubtrees(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1, 5: 2, 10: 3})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 7, 5: 22, 10: 33})
	old, next := env.manifests(t, oldTree, newTree, 1)
	revoked, err := newRevocationSet([]Interval{{Lo: 4, Hi: 8}, {Lo: 8, Hi: 12}})
	if err != nil {
		t.Fatal(err)
	}
	proof, result, _, err := buildWithStats(env, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
		Revoked:    revoked,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BlindRevoked < 2 {
		t.Fatalf("expected at least two blind revoked terminals, got %d", result.BlindRevoked)
	}
	kinds := map[uint64]EventKind{}
	for _, ev := range result.Events {
		kinds[ev.Key] = ev.Kind
	}
	if len(kinds) != 1 || kinds[1] != EventChanged {
		t.Fatalf("only key 1 change should be visible, got %v", kinds)
	}
	for _, rec := range proof.Records {
		if (rec.key == 5 || rec.key == 10) &&
			(rec.kind == recLeafUpdate || rec.kind == recLeafShared) {
			t.Fatalf("revoked key %d disclosed", rec.key)
		}
	}
}

// TestEmptyAuthorization 空授权集合下整棵树都以区间外盲终端处理。
func TestEmptyAuthorization(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, result, _, err := buildWithStats(env, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: IntervalSet{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 0 {
		t.Fatalf("no events allowed under empty authorization")
	}
	if result.BlindOutside == 0 {
		t.Fatalf("whole tree must be blind outside")
	}
	for _, rec := range proof.Records {
		if rec.kind == recLeafUpdate || rec.kind == recLeafShared {
			t.Fatalf("no explainable leaf records allowed")
		}
	}
}
