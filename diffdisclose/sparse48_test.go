package diffdisclose

import (
	"math/rand"
	"testing"
)

// TestSparse48DoesNotExpandAllKeys 在 48 位稀疏空间放置少量叶子，
// 只授权一个窄区间，断言证明访问节点数与树高同阶，而不是 2^48。
func TestSparse48DoesNotExpandAllKeys(t *testing.T) {
	env := newTestEnv(t)
	const height = MaxHeight

	// 选择靠近高端的键，验证 48 位地址导航正确。
	base := uint64(1) << 44
	oldTree := treeFromMap(t, height, map[uint64]uint64{
		base + 1: 1,
		base + 2: 2,
		base + 9: 9,
	})
	newTree := treeFromMap(t, height, map[uint64]uint64{
		base + 1:  1, // 未变化
		base + 2:  22,
		base + 10: 10,
	})
	old, next := env.manifests(t, oldTree, newTree, 1)

	// 仅授权覆盖 base..base+8 的对齐窄区间（高度 3 子树），
	// base+9 落在区间外，验证其只以盲摘要出现。
	authorized := mustSet(t, Interval{Lo: base, Hi: base + 8})
	proof, result, stats, err := buildWithStats(env, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: authorized,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 访问节点数受树高加窄区间深度约束，与 2^48 完全无关。
	if stats.VisitedNodes > 4*height+32 {
		t.Fatalf("48-bit proof expanded too many nodes: %d", stats.VisitedNodes)
	}
	if len(proof.Records) > 4*height+32 {
		t.Fatalf("48-bit proof has too many records: %d", len(proof.Records))
	}

	// 区间外的键 base+9（在授权外）绝不产生事件。
	for _, ev := range result.Events {
		if ev.Key < base || ev.Key >= base+16 {
			t.Fatalf("unauthorized key %d leaked", ev.Key)
		}
	}
	if result.BlindOutside == 0 {
		t.Fatalf("the vast outside space must be represented by blind terminals")
	}

	// 差分事件：授权内只有 base+2 改变；base+9、base+10 均在授权外不可见。
	kinds := map[uint64]EventKind{}
	for _, ev := range result.Events {
		kinds[ev.Key] = ev.Kind
	}
	if len(kinds) != 1 || kinds[base+2] != EventChanged {
		t.Fatalf("unexpected events: %v", kinds)
	}
	if _, leaked := kinds[base+9]; leaked {
		t.Fatalf("outside key base+9 must not appear")
	}
	if _, leaked := kinds[base+10]; leaked {
		t.Fatalf("outside key base+10 must not appear")
	}
}

// TestSparse48RevocationAndCache 在 48 位空间验证撤权盲化与缓存省略，
// 并确认规模仍与树高同阶。
func TestSparse48RevocationAndCache(t *testing.T) {
	env := newTestEnv(t)
	const height = MaxHeight
	base := uint64(1) << 40
	oldTree := treeFromMap(t, height, map[uint64]uint64{
		base + 1: 1, base + 5: 5, base + 6: 6,
	})
	newTree := treeFromMap(t, height, map[uint64]uint64{
		base + 1: 2, base + 5: 55, base + 6: 66,
	})
	old, next := env.manifests(t, oldTree, newTree, 1)

	authorized := mustSet(t, Interval{Lo: base, Hi: base + 8})
	// 撤权对齐子树 [base+4, base+8)，高度 2。
	revoked := mustSet(t, Interval{Lo: base + 4, Hi: base + 8})

	// 为未变化路径之外提供一条叶子缓存（base+1 在新树的地址）。
	leafAddr := fullLeafAddr(height, base+1)
	cache := []CacheEntry{{
		Manifest: newTree.Root(),
		Side:     CacheNew,
		Address:  leafAddr,
		Height:   0,
		Digest:   newTree.digest(0, leafAddr),
	}}
	proof, result, stats, err := buildWithStats(env, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: authorized,
		Revoked:    revoked,
		Cache:      cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.VisitedNodes > 4*height+32 {
		t.Fatalf("revocation proof too large: %d", stats.VisitedNodes)
	}
	if result.BlindRevoked == 0 {
		t.Fatalf("revoked 48-bit subtree must produce a blind terminal")
	}
	for _, rec := range proof.Records {
		if (rec.key == base+5 || rec.key == base+6) &&
			(rec.kind == recLeafUpdate || rec.kind == recLeafShared) {
			t.Fatalf("revoked key %d disclosed", rec.key)
		}
	}
}

// TestSparse48Randomized 多轮随机窄授权，断言规模上界稳定。
func TestSparse48Randomized(t *testing.T) {
	env := newTestEnv(t)
	const height = MaxHeight
	rng := rand.New(rand.NewSource(48))
	for round := 0; round < 25; round++ {
		base := uint64(rng.Int63n(1 << 46))
		base &= ^uint64(15) // 16 对齐
		oldMap := map[uint64]uint64{base + 1: rng.Uint64(), base + 2: rng.Uint64()}
		newMap := map[uint64]uint64{base + 1: rng.Uint64(), base + 3: rng.Uint64()}
		oldTree := treeFromMap(t, height, oldMap)
		newTree := treeFromMap(t, height, newMap)
		old, next := env.manifests(t, oldTree, newTree, 1)
		authorized := mustSet(t, Interval{Lo: base, Hi: base + 16})
		_, _, stats, err := buildWithStats(env, BuildRequest{
			OldTree: oldTree, NewTree: newTree, Old: old, New: next,
			Authorized: authorized,
		})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if stats.VisitedNodes > 4*height+32 {
			t.Fatalf("round %d expanded %d nodes", round, stats.VisitedNodes)
		}
	}
}

// buildWithStats 构造并完成编解码与验证，返回证明、结果与统计。
func buildWithStats(env *testEnv, req BuildRequest) (*Proof, VerifyResult, ProofStats, error) {
	proof, stats, err := BuildProof(req)
	if err != nil {
		return nil, VerifyResult{}, ProofStats{}, err
	}
	dec, err := DecodeProof(proof.Encode())
	if err != nil {
		return nil, VerifyResult{}, ProofStats{}, err
	}
	res, err := dec.Verify(VerifyOptions{Verifier: env.verifier})
	if err != nil {
		return nil, VerifyResult{}, ProofStats{}, err
	}
	return dec, res, stats, nil
}
