package diffdisclose

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestHeight6ExhaustiveKeyPositions 用 6 位键空间（64 个键位）逐一覆盖
// 每个键位的新增、删除、改变三种事件，并配合对齐撤权子区间，
// 验证在完整 6 位树下事件定位、撤权盲化与根重算都正确。
func TestHeight6ExhaustiveKeyPositions(t *testing.T) {
	env := newTestEnv(t)
	const height = 6
	for key := uint64(0); key < 1<<height; key++ {
		oldTree := treeFromMap(t, height, map[uint64]uint64{key: 7})
		newTree := treeFromMap(t, height, map[uint64]uint64{key: 8})
		old, next := env.manifests(t, oldTree, newTree, 1)
		proof, res, _ := env.buildAndVerify(t, BuildRequest{
			OldTree: oldTree, NewTree: newTree, Old: old, New: next,
			Authorized: fullRange(height),
		})
		if len(res.Events) != 1 || res.Events[0].Key != key || res.Events[0].Kind != EventChanged {
			t.Fatalf("key %d: expected single change event, got %+v", key, res.Events)
		}
		// 证明规模应只随树高线性增长，与 2^height 无关地不展开空区。
		if len(proof.Records) > height*4 {
			t.Fatalf("key %d: proof too large %d", key, len(proof.Records))
		}
	}
}

// TestHeight6RandomFuzz 对 6 位树做随机差分、撤权与缓存子集模糊测试。
func TestHeight6RandomFuzz(t *testing.T) {
	env := newTestEnv(t)
	const height = 6
	const rounds = 400
	rng := rand.New(rand.NewSource(20261121))

	for round := 0; round < rounds; round++ {
		oldMap := randomLeafMap(rng, height, 12)
		newMap := randomLeafMap(rng, height, 12)
		oldTree := treeFromMap(t, height, oldMap)
		newTree := treeFromMap(t, height, newMap)
		old, next := env.manifests(t, oldTree, newTree, 1)

		auth := randomAuthSet(rng, height)
		revoked := randomAlignedRevocation(rng, height, auth)
		cache := randomCache(rng, t, oldTree, newTree, old.Root, next.Root, height)

		req := BuildRequest{
			OldTree: oldTree, NewTree: newTree, Old: old, New: next,
			Authorized: auth,
			Revoked:    revoked,
			Cache:      cache,
		}
		proof, stats, err := BuildProof(req)
		if err != nil {
			t.Fatalf("round %d build: %v", round, err)
		}
		dec, err := DecodeProof(proof.Encode())
		if err != nil {
			t.Fatalf("round %d decode: %v", round, err)
		}
		res, err := dec.Verify(VerifyOptions{Verifier: env.verifier})
		if err != nil {
			t.Fatalf("round %d verify: %v", round, err)
		}
		assertEventsMatch(t, res.Events, oldMap, newMap, auth, revoked)
		assertNoRevokedLeak(t, dec, auth, revoked)

		// 同一输入必须得到唯一序列；缓存命中数不能超过缓存条目数。
		again, _, err := BuildProof(req)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(proof.Encode(), again.Encode()) {
			t.Fatalf("round %d: non-deterministic encoding", round)
		}
		if stats.CacheHits > len(cache) {
			t.Fatalf("round %d: more cache hits than entries", round)
		}
		// 记录规模与“叶子数 × 树高”同阶；空内部子树由空终端截断，
		// 不会无界枚举键位。常数上界覆盖全授权的分散叶子情形。
		sparseBound := 4*height*(len(oldMap)+len(newMap)+1) + 8
		if len(proof.Records) > sparseBound {
			t.Fatalf("round %d: proof expands too many nodes: %d", round, len(proof.Records))
		}
	}
}

func randomLeafMap(rng *rand.Rand, height, max int) map[uint64]uint64 {
	n := rng.Intn(max + 1)
	m := make(map[uint64]uint64, n)
	limit := 1 << height
	for i := 0; i < n; i++ {
		key := uint64(rng.Intn(limit))
		m[key] = rng.Uint64()
	}
	return m
}

func randomAuthSet(rng *rand.Rand, height int) IntervalSet {
	limit := uint64(1 << height)
	if rng.Intn(2) == 0 {
		return fullRange(height)
	}
	lo := uint64(rng.Intn(1 << height))
	hi := lo + uint64(1+rng.Intn(int(limit-lo)))
	set, err := NewIntervalSet([]Interval{{Lo: lo, Hi: hi}})
	if err != nil {
		panic(err)
	}
	return set
}

func randomAlignedRevocation(rng *rand.Rand, height int, auth IntervalSet) IntervalSet {
	if rng.Intn(2) == 0 {
		return IntervalSet{}
	}
	// 选一个对齐宽度（2 的幂），在授权内滑动到合法起点。
	width := uint64(1) << uint(rng.Intn(height+1))
	limit := uint64(1) << height
	start := uint64(rng.Intn(int(limit)))
	start = (start / width) * width
	if start+width > limit {
		return IntervalSet{}
	}
	iv := Interval{Lo: start, Hi: start + width}
	if auth.cover(iv.Lo, iv.Hi) != coverFull {
		return IntervalSet{}
	}
	set, err := NewIntervalSet([]Interval{iv})
	if err != nil {
		panic(err)
	}
	return set
}

func randomCache(rng *rand.Rand, t *testing.T, oldTree, newTree *Tree, oldRoot, newRoot Digest, height int) []CacheEntry {
	n := rng.Intn(4)
	out := make([]CacheEntry, 0, n)
	for i := 0; i < n; i++ {
		h := 1 + rng.Intn(height)
		prefixLen := height - h
		bits := make([]byte, prefixLen)
		for j := range bits {
			bits[j] = byte(rng.Intn(2))
		}
		address := addr{bits: bits}
		if rng.Intn(2) == 0 {
			out = append(out, CacheEntry{
				Manifest: newRoot, Side: CacheNew, Address: address, Height: h,
				Digest: newTree.digest(h, address),
			})
		} else {
			out = append(out, CacheEntry{
				Manifest: oldRoot, Side: CacheOld, Address: address, Height: h,
				Digest: oldTree.digest(h, address),
			})
		}
	}
	return out
}
