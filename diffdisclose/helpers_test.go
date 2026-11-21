package diffdisclose

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

// testEnv 封装一次测试所需的签名器、清单与常用构造工具。
type testEnv struct {
	signer   *Ed25519Signer
	verifier Ed25519Verifier
	tenant   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	signer, err := NewEd25519Signer()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return &testEnv{signer: signer, verifier: Ed25519Verifier{}, tenant: "tenant-a"}
}

// saltFor 用确定性种子生成 32 字节盐，保证测试可复现。
func saltFor(seed uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], seed)
	sum := sha256.Sum256(append([]byte("salt:"), b[:]...))
	return sum[:]
}

// valueFor 生成确定性值。
func valueFor(seed uint64) []byte {
	return []byte(fmt.Sprintf("value-%d", seed))
}

// treeFromMap 用键->值种子映射构造稀疏树。
func treeFromMap(t *testing.T, height int, m map[uint64]uint64) *Tree {
	t.Helper()
	leaves := make([]Leaf, 0, len(m))
	for key, seed := range m {
		leaves = append(leaves, Leaf{Key: key, Value: valueFor(seed), Salt: saltFor(seed)})
	}
	tree, err := NewTree(height, leaves)
	if err != nil {
		t.Fatalf("NewTree: %v", err)
	}
	return tree
}

// manifests 构造一对父子签名清单。
func (e *testEnv) manifests(t *testing.T, oldTree, newTree *Tree, version uint64) (Manifest, Manifest) {
	t.Helper()
	oldRoot := Digest{}
	if oldTree != nil {
		oldRoot = oldTree.Root()
	}
	old, err := SignManifest(e.signer, e.tenant, version-1, Digest{}, oldRoot)
	if err != nil {
		t.Fatalf("sign old: %v", err)
	}
	new, err := SignManifest(e.signer, e.tenant, version, old.digest(), newTree.Root())
	if err != nil {
		t.Fatalf("sign new: %v", err)
	}
	return old, new
}

// fullRange 返回整棵树的授权区间。
func fullRange(height int) IntervalSet {
	set, _ := NewIntervalSet([]Interval{{Lo: 0, Hi: uint64(1) << height}})
	return set
}

// mustSet 构造区间集合并在错误时终止。
func mustSet(t *testing.T, ivs ...Interval) IntervalSet {
	t.Helper()
	set, err := NewIntervalSet(ivs)
	if err != nil {
		t.Fatalf("interval set: %v", err)
	}
	return set
}

// buildAndVerify 构造、编码、解码并验证证明，返回解码后的证明与结果。
func (e *testEnv) buildAndVerify(t *testing.T, req BuildRequest) (*Proof, VerifyResult, ProofStats) {
	t.Helper()
	proof, stats, err := BuildProof(req)
	if err != nil {
		t.Fatalf("BuildProof: %v", err)
	}
	encoded := proof.Encode()
	decoded, err := DecodeProof(encoded)
	if err != nil {
		t.Fatalf("DecodeProof: %v", err)
	}
	if len(encoded) == 0 || string(encoded[:len(magic)]) != string(magic) {
		t.Fatalf("missing magic")
	}
	result, err := decoded.Verify(VerifyOptions{Verifier: e.verifier})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return decoded, result, stats
}

// cacheForTree 为指定地址集合生成真实缓存条目。
func cacheForTree(root Digest, side CacheSide, tree *Tree, addrs []struct {
	height int
	addr   addr
}) []CacheEntry {
	out := make([]CacheEntry, 0, len(addrs))
	for _, item := range addrs {
		out = append(out, CacheEntry{
			Manifest: root,
			Side:     side,
			Address:  item.addr,
			Height:   item.height,
			Digest:   tree.digest(item.height, item.addr),
		})
	}
	return out
}
