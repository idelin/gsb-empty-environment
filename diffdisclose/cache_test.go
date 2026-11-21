package diffdisclose

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

// addrAt 用 MSB 优先位串构造地址。
func addrAt(bits ...byte) addr { return addr{bits: bits} }

func TestAuthenticatedCacheOmitsSubtree(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1, 2: 2, 9: 9})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 1, 2: 2, 9: 19})
	old, next := env.manifests(t, oldTree, newTree, 1)

	// 旧新相等的左半子树（键 0..7）提供一条真实内部缓存。
	leftAddr := addrAt(0)
	cache := []CacheEntry{{
		Manifest: newTree.Root(),
		Side:     CacheNew,
		Address:  leftAddr,
		Height:   3,
		Digest:   newTree.digest(3, leftAddr),
	}}
	proof, result, stats := env.buildAndVerify(t, BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
		Cache:      cache,
	})
	if stats.CacheHits == 0 || result.CacheHits == 0 {
		t.Fatalf("authenticated cache must be used to omit a subtree")
	}
	// 被省略子树内的值不得出现在任何叶子载荷记录里。
	for _, rec := range proof.Records {
		if rec.newLeaf != nil && (rec.newLeaf.Key == 1 || rec.newLeaf.Key == 2) {
			t.Fatalf("cached subtree leaf value disclosed for key %d", rec.newLeaf.Key)
		}
		if rec.oldLeaf != nil && (rec.oldLeaf.Key == 1 || rec.oldLeaf.Key == 2) {
			t.Fatalf("cached subtree leaf value disclosed for key %d", rec.oldLeaf.Key)
		}
	}
}

func TestRejectExternalCacheImpersonation(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)

	// 外部清单摘要（既非旧根也非新根）的缓存必须被拒绝。
	foreign := sha256Sum([]byte("attacker-manifest"))
	bad := []CacheEntry{{
		Manifest: foreign,
		Side:     CacheNew,
		Address:  addrAt(0),
		Height:   3,
		Digest:   newTree.digest(3, addrAt(0)),
	}}
	_, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
		Cache:      bad,
	})
	if !errors.Is(err, errBadCacheEntry) {
		t.Fatalf("external cache accepted: %v", err)
	}

	// 引用可信清单但摘要伪造：构造器在使用点比对失败，静默忽略，
	// 不会产生任何缓存命中，证明仍可正常验证。
	forged := []CacheEntry{{
		Manifest: newTree.Root(),
		Side:     CacheNew,
		Address:  addrAt(0),
		Height:   3,
		Digest:   sha256Sum([]byte("forged-digest")),
	}}
	proofForged, statsForged, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
		Cache:      forged,
	})
	if err != nil {
		t.Fatalf("mismatched cache should be ignored, got %v", err)
	}
	if statsForged.CacheHits != 0 {
		t.Fatalf("forged cache must never produce a cache hit")
	}

	// 传输中把已认证缓存的摘要改成伪造值，验证者必须拒绝冒充。
	// 选择旧新相等的左半子树（键 0..7），该缓存会被证明真正引用。
	stableOld := treeFromMap(t, height, map[uint64]uint64{1: 1, 12: 12})
	stableNew := treeFromMap(t, height, map[uint64]uint64{1: 1, 12: 99})
	stableOldM, stableNewM := env.manifests(t, stableOld, stableNew, 1)
	good := []CacheEntry{{
		Manifest: stableNew.Root(),
		Side:     CacheNew,
		Address:  addrAt(0),
		Height:   3,
		Digest:   stableNew.digest(3, addrAt(0)),
	}}
	proof, stats, err := BuildProof(BuildRequest{
		OldTree: stableOld, NewTree: stableNew, Old: stableOldM, New: stableNewM,
		Authorized: fullRange(height),
		Cache:      good,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.CacheHits == 0 {
		t.Fatalf("test setup: cache must be referenced by the proof")
	}
	tampered := *proof
	tampered.Cache = make([]CacheEntry, len(proof.Cache))
	copy(tampered.Cache, proof.Cache)
	tampered.Cache[0].Digest = sha256Sum([]byte("forged-digest"))
	dec, derr := DecodeProof(tampered.Encode())
	if derr != nil {
		t.Fatal(derr)
	}
	if _, err := dec.Verify(VerifyOptions{Verifier: env.verifier}); !errors.Is(err, errBadCacheEntry) && !errors.Is(err, errRootMismatch) {
		t.Fatalf("tampered cache should fail verification, got %v", err)
	}
	_ = proofForged
}

func TestCacheSubsetDeterminism(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree := treeFromMap(t, height, map[uint64]uint64{0: 0, 1: 1, 2: 2, 3: 3})
	newTree := treeFromMap(t, height, map[uint64]uint64{0: 0, 1: 1, 2: 22, 3: 33})
	old, next := env.manifests(t, oldTree, newTree, 1)

	entry := func(side CacheSide, root Digest, bits []byte, h int) CacheEntry {
		a := addrAt(bits...)
		tree := newTree
		if side == CacheOld {
			tree = oldTree
		}
		return CacheEntry{Manifest: root, Side: side, Address: a, Height: h, Digest: tree.digest(h, a)}
	}
	full := []CacheEntry{
		entry(CacheNew, newTree.Root(), []byte{0, 0}, 2),
		entry(CacheOld, oldTree.Root(), []byte{0, 1}, 2),
	}
	req := BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
	}
	req.Cache = full
	p1, _, err := BuildProof(req)
	if err != nil {
		t.Fatal(err)
	}
	// 同一缓存子集重复构造，编码必须逐字节一致。
	p2, _, err := BuildProof(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p1.Encode(), p2.Encode()) {
		t.Fatalf("same authenticated cache must yield identical encoding")
	}
	// 更大的已认证缓存只能减少披露，得到不同但仍确定的序列。
	req.Cache = full[:1]
	p3, _, err := BuildProof(req)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(p3.Encode(), p1.Encode()) {
		t.Fatalf("different authenticated cache sets should change the minimal sequence")
	}
	p4, _, _ := BuildProof(req)
	if !bytes.Equal(p3.Encode(), p4.Encode()) {
		t.Fatalf("proof must be stable across repeated builds with the same cache")
	}
}

// sha256Sum 是测试用的摘要辅助。
func sha256Sum(data []byte) Digest {
	return sha256.Sum256(data)
}
