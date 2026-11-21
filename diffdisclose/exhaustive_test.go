package diffdisclose

import (
	"bytes"
	"testing"
)

// allSubsets 返回 n 个元素的所有子集（每个子集是下标切片）。
func allSubsets(n int) [][]int {
	total := 1 << n
	out := make([][]int, 0, total)
	for mask := 0; mask < total; mask++ {
		set := make([]int, 0)
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				set = append(set, i)
			}
		}
		out = append(out, set)
	}
	return out
}

// TestExhaustiveHeight2 对高度 2（4 个键位）穷举：
// 旧/新叶子子集、授权区间、撤权子区间与缓存子集的组合，
// 验证差分事件与真实集合差异一致、证明确定且验证通过。
func TestExhaustiveHeight2(t *testing.T) {
	env := newTestEnv(t)
	const height = 2
	keys := []uint64{0, 1, 2, 3}
	leafSets := allSubsets(len(keys))

	// 所有对齐到子树的撤权候选（高度 1 与高度 0）。
	revokeCandidates := []IntervalSet{
		{},
		mustSet(t, Interval{Lo: 0, Hi: 2}),
		mustSet(t, Interval{Lo: 2, Hi: 4}),
		mustSet(t, Interval{Lo: 0, Hi: 1}),
		mustSet(t, Interval{Lo: 1, Hi: 2}),
	}
	authCandidates := []IntervalSet{
		fullRange(height),
		mustSet(t, Interval{Lo: 0, Hi: 2}),
		mustSet(t, Interval{Lo: 2, Hi: 4}),
		mustSet(t, Interval{Lo: 1, Hi: 3}),
	}

	iterations := 0
	for _, oldIdx := range leafSets {
		for _, newIdx := range leafSets {
			oldMap := seedsFromIndices(keys, oldIdx, 1)
			newMap := seedsFromIndices(keys, newIdx, 2)
			oldTree := treeFromMap(t, height, oldMap)
			newTree := treeFromMap(t, height, newMap)
			old, next := env.manifests(t, oldTree, newTree, 1)
			for _, auth := range authCandidates {
				for _, revoked := range revokeCandidates {
					if !revocationWithin(auth, revoked) {
						continue
					}
					cache := subsetCache(t, oldTree, newTree, old.Root, next.Root, height, (iterations%4)+1)
					req := BuildRequest{
						OldTree: oldTree, NewTree: newTree, Old: old, New: next,
						Authorized: auth,
						Revoked:    revoked,
						Cache:      cache,
					}
					proof, _, err := BuildProof(req)
					if err != nil {
						t.Fatalf("build err %v old=%v new=%v", err, oldIdx, newIdx)
					}
					dec, err := DecodeProof(proof.Encode())
					if err != nil {
						t.Fatalf("decode: %v", err)
					}
					res, err := dec.Verify(VerifyOptions{Verifier: env.verifier})
					if err != nil {
						t.Fatalf("verify err %v old=%v new=%v auth=%v rev=%v", err, oldIdx, newIdx, auth.items, revoked.items)
					}
					assertEventsMatch(t, res.Events, oldMap, newMap, auth, revoked)
					assertNoRevokedLeak(t, dec, auth, revoked)
					// 规范唯一性：同输入重复构造编码一致。
					again, _, err := BuildProof(req)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(proof.Encode(), again.Encode()) {
						t.Fatalf("non-deterministic proof")
					}
					iterations++
				}
			}
		}
	}
	t.Logf("exhaustive height-2 iterations=%d", iterations)
}

// seedsFromIndices 把下标子集转成键->值种子映射，新旧代际用不同种子基。
func seedsFromIndices(keys []uint64, idx []int, base uint64) map[uint64]uint64 {
	m := make(map[uint64]uint64)
	for _, i := range idx {
		m[keys[i]] = base*100 + keys[i]
	}
	return m
}

// revocationWithin 判断撤权集合是否完整落在授权集合内。
func revocationWithin(auth, revoked IntervalSet) bool {
	for _, iv := range revoked.items {
		if auth.cover(iv.Lo, iv.Hi) != coverFull {
			return false
		}
	}
	return true
}

// subsetCache 生成确定性的真实缓存子集，覆盖内部与叶子地址。
func subsetCache(t *testing.T, oldTree, newTree *Tree, oldRoot, newRoot Digest, height, variant int) []CacheEntry {
	t.Helper()
	candidates := []CacheEntry{
		{Manifest: newRoot, Side: CacheNew, Address: addrAt(0), Height: height - 1, Digest: newTree.digest(height-1, addrAt(0))},
		{Manifest: newRoot, Side: CacheNew, Address: addrAt(1), Height: height - 1, Digest: newTree.digest(height-1, addrAt(1))},
		{Manifest: oldRoot, Side: CacheOld, Address: addrAt(0, 0), Height: height - 2, Digest: oldTree.digest(height-2, addrAt(0, 0))},
	}
	if height-2 == 0 {
		candidates[2].Address = fullLeafAddr(height, 0)
		candidates[2].Height = 0
		candidates[2].Digest = oldTree.digest(0, candidates[2].Address)
	}
	switch variant {
	case 1:
		return nil
	case 2:
		return candidates[:1]
	case 3:
		return candidates[:2]
	default:
		return candidates
	}
}

// fullLeafAddr 返回键 key 的完整叶子地址（树高位优先）。
func fullLeafAddr(height int, key uint64) addr {
	bits := make([]byte, height)
	for i := 0; i < height; i++ {
		bits[i] = byte((key >> (height - 1 - i)) & 1)
	}
	return addr{bits: bits}
}

// assertEventsMatch 验证事件集合恰好等于授权未撤回范围内的真实差分。
func assertEventsMatch(t *testing.T, events []Event, oldMap, newMap map[uint64]uint64, auth, revoked IntervalSet) {
	t.Helper()
	want := map[uint64]EventKind{}
	for key := range unionKeys(oldMap, newMap) {
		if !auth.contains(key) || revoked.contains(key) {
			continue
		}
		_, oldOK := oldMap[key]
		_, newOK := newMap[key]
		switch {
		case !oldOK && newOK:
			want[key] = EventAdded
		case oldOK && !newOK:
			want[key] = EventRemoved
		case oldOK && newOK && oldMap[key] != newMap[key]:
			want[key] = EventChanged
		}
	}
	got := map[uint64]EventKind{}
	for _, ev := range events {
		if _, dup := got[ev.Key]; dup {
			t.Fatalf("duplicate event for %d", ev.Key)
		}
		got[ev.Key] = ev.Kind
	}
	if len(got) != len(want) {
		t.Fatalf("event count got=%d want=%d (got=%v want=%v)", len(got), len(want), got, want)
	}
	for key, kind := range want {
		if got[key] != kind {
			t.Fatalf("key %d got=%v want=%v", key, got[key], kind)
		}
	}
}

func unionKeys(a, b map[uint64]uint64) map[uint64]struct{} {
	out := make(map[uint64]struct{})
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// assertNoRevokedLeak 验证撤回键不以可解释记录出现。
func assertNoRevokedLeak(t *testing.T, p *Proof, auth, revoked IntervalSet) {
	t.Helper()
	for _, rec := range p.Records {
		if rec.kind == recLeafUpdate || rec.kind == recLeafShared || rec.kind == recCachedLeaf {
			if revoked.contains(rec.key) {
				t.Fatalf("revoked key %d leaked via record kind %d", rec.key, rec.kind)
			}
		}
	}
}
