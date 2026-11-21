package diffdisclose

import (
	"bytes"
	"errors"
	"testing"
)

// TestRevokedSubtreeOnlyRevealsOpaqueDigests 验证撤权区间不展开任何
// 键或值，也不通过记录形状透露先前键是否仍存在。
func TestRevokedSubtreeOnlyRevealsOpaqueDigests(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	// 撤回子树 [4,8)，对齐高度 2。
	revoked := mustSet(t, Interval{Lo: 4, Hi: 8})
	oldTree := treeFromMap(t, height, map[uint64]uint64{
		1: 10,
		5: 50, // 撤回区间内，旧树存在
		6: 60,
	})
	newTree := treeFromMap(t, height, map[uint64]uint64{
		1: 11,
		5: 50, // 撤回区间内，新树仍存在，但客户端不得得知
		7: 70,
	})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, result, _ := env.buildAndVerify(t, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
		Revoked:    revoked,
	})

	// 撤回键 5、6、7 绝不能出现在任何事件或叶子载荷中。
	for _, ev := range result.Events {
		if ev.Key >= 4 && ev.Key < 8 {
			t.Fatalf("revoked key %d leaked as event", ev.Key)
		}
	}
	for _, rec := range proof.Records {
		if rec.key >= 4 && rec.key < 8 &&
			(rec.kind == recLeafUpdate || rec.kind == recLeafShared || rec.kind == recCachedLeaf) {
			t.Fatalf("revoked key %d leaked in record kind %d", rec.key, rec.kind)
		}
		if rec.oldLeaf != nil && rec.oldLeaf.Key >= 4 && rec.oldLeaf.Key < 8 {
			t.Fatalf("revoked old value leaked")
		}
		if rec.newLeaf != nil && rec.newLeaf.Key >= 4 && rec.newLeaf.Key < 8 {
			t.Fatalf("revoked new value leaked")
		}
	}
	if result.BlindRevoked == 0 {
		t.Fatalf("revoked subtree must produce a blind terminal")
	}

	// 编码字节里不得出现撤回区间内的值。
	encoded := proof.Encode()
	if bytes.Contains(encoded, valueFor(50)) || bytes.Contains(encoded, valueFor(60)) ||
		bytes.Contains(encoded, valueFor(70)) {
		t.Fatalf("revoked values present in encoded proof")
	}
}

// TestRevokedExistenceIndistinguishable 比较“撤回键仍存在”与“撤回键已
// 删除”两种世界：撤回子树的盲记录形状与负载长度必须完全一致。
func TestRevokedExistenceIndistinguishable(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	revoked := mustSet(t, Interval{Lo: 4, Hi: 8})

	build := func(newSeeds map[uint64]uint64) []record {
		oldTree := treeFromMap(t, height, map[uint64]uint64{5: 1, 6: 2})
		newTree := treeFromMap(t, height, newSeeds)
		old, next := env.manifests(t, oldTree, newTree, 1)
		proof, _, err := BuildProof(BuildRequest{
			OldTree: oldTree, NewTree: newTree, Old: old, New: next,
			Authorized: fullRange(height),
			Revoked:    revoked,
		})
		if err != nil {
			t.Fatal(err)
		}
		blind := make([]record, 0)
		for _, rec := range proof.Records {
			if rec.kind == recBlindRevoked {
				blind = append(blind, rec)
			}
		}
		return blind
	}

	// 两个世界的撤回子树都非空，但键集合不同，盲记录形状仍须一致。
	stillPresent := build(map[uint64]uint64{5: 1, 6: 2})
	allDeleted := build(map[uint64]uint64{7: 3})
	if len(stillPresent) != len(allDeleted) || len(stillPresent) == 0 {
		t.Fatalf("blind record count differs: %d vs %d", len(stillPresent), len(allDeleted))
	}
	for i := range stillPresent {
		a, b := stillPresent[i], allDeleted[i]
		if a.kind != b.kind {
			t.Fatalf("blind record shape differs")
		}
	}
}

// TestRevocationMustBeSubtreeAligned 验证非对齐或越界撤权被拒绝。
func TestRevocationMustBeSubtreeAligned(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)

	bad := []IntervalSet{
		mustSet(t, Interval{Lo: 4, Hi: 7}), // 宽度非 2 的幂
		mustSet(t, Interval{Lo: 3, Hi: 7}), // 起点未对齐
		mustSet(t, Interval{Lo: 12, Hi: 20}),
	}
	for _, revoked := range bad {
		_, _, err := BuildProof(BuildRequest{
			OldTree: oldTree, NewTree: newTree, Old: old, New: next,
			Authorized: fullRange(height),
			Revoked:    revoked,
		})
		if !errors.Is(err, errRevocationAligned) && !errors.Is(err, errRevocationScope) {
			t.Fatalf("bad revocation accepted: %v", err)
		}
	}

	// 撤权区间落在授权集合外必须拒绝。
	outside := mustSet(t, Interval{Lo: 8, Hi: 16})
	authorized := mustSet(t, Interval{Lo: 0, Hi: 8})
	_, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: authorized,
		Revoked:    outside,
	})
	if !errors.Is(err, errRevocationScope) {
		t.Fatalf("out-of-scope revocation accepted: %v", err)
	}
}

// TestOutsideAuthorizationOnlyBlind 验证授权区间外只给不可解释摘要。
func TestOutsideAuthorizationOnlyBlind(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{2: 1, 12: 99})
	newTree := treeFromMap(t, height, map[uint64]uint64{2: 2, 12: 100})
	old, next := env.manifests(t, oldTree, newTree, 1)
	authorized := mustSet(t, Interval{Lo: 0, Hi: 8})
	proof, result, _ := env.buildAndVerify(t, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: authorized,
	})
	for _, ev := range result.Events {
		if ev.Key >= 8 {
			t.Fatalf("unauthorized key %d leaked", ev.Key)
		}
	}
	for _, rec := range proof.Records {
		if rec.key >= 8 && (rec.kind == recLeafUpdate || rec.kind == recLeafShared) {
			t.Fatalf("unauthorized leaf disclosed")
		}
	}
	if result.BlindOutside == 0 {
		t.Fatalf("outside region must produce a blind terminal")
	}
}
