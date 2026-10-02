package mpt

import (
	"bytes"
	"testing"
)

func buildTrie(t *testing.T) *Trie {
	t.Helper()
	trie := New()
	for _, p := range [][2]string{
		{"account.balance", "100"},
		{"account.nonce", "5"},
		{"account.storage.code", "0x6080"},
		{"account.storage.slot0", "0x00"},
		{"profile.name", "alice"},
	} {
		trie.Put(p[0], []byte(p[1]))
	}
	return trie
}

func TestInsertAndGet(t *testing.T) {
	trie := buildTrie(t)
	cases := map[string]string{
		"account.balance":       "100",
		"account.nonce":         "5",
		"account.storage.code":  "0x6080",
		"account.storage.slot0": "0x00",
		"profile.name":          "alice",
	}
	for k, want := range cases {
		got, ok := trie.Get(k)
		if !ok || string(got) != want {
			t.Fatalf("Get(%q) = %q, %v; want %q", k, got, ok, want)
		}
	}
	if _, ok := trie.Get("account.missing"); ok {
		t.Fatal("Get 对不存在的键应返回 false")
	}
}

func TestUpdateSameKey(t *testing.T) {
	trie := buildTrie(t)
	_, _, before := trie.RangeSummary()
	trie.Put("account.balance", []byte("250"))
	got, ok := trie.Get("account.balance")
	if !ok || string(got) != "250" {
		t.Fatalf("更新后 Get = %q, %v; want 250", got, ok)
	}
	_, _, after := trie.RangeSummary()
	if after != before {
		t.Fatalf("同键更新不应改变键数量: before=%d after=%d", before, after)
	}
}

// TestPathCompression 验证公共前缀被收缩进 Extension 节点。
func TestPathCompression(t *testing.T) {
	trie := New()
	trie.Put("account.balance", []byte("100"))
	trie.Put("account.nonce", []byte("5"))

	ext, ok := trie.root.(*Extension)
	if !ok {
		t.Fatalf("根应为 Extension 节点，实际 %T", trie.root)
	}
	// "account.balance" 与 "account.nonce" 的公共 nibble 前缀为
	// "account." 加上 'b'/'n' 共同的高半字节 0x6。
	want := "6163636f756e742e" + "6"
	if got := nibblesHex(ext.Path); got != want {
		t.Fatalf("共享路径应压缩为 %q，实际 %q", want, got)
	}
	branch, ok := ext.Child.(*Branch)
	if !ok {
		t.Fatalf("Extension 子节点应为 Branch，实际 %T", ext.Child)
	}
	occupied := 0
	for _, c := range branch.Children {
		if c != nil {
			occupied++
		}
	}
	if occupied != 2 {
		t.Fatalf("Branch 应有 2 个非空槽位（balance/nonce 分叉），实际 %d", occupied)
	}
	for _, c := range branch.Children {
		if c == nil {
			continue
		}
		if _, ok := c.(*Leaf); !ok {
			t.Fatalf("分叉后应直接为 Leaf，实际 %T", c)
		}
	}
}

// TestExtensionSplit 验证在共享路径中间插入时 Extension 被正确拆分。
func TestExtensionSplit(t *testing.T) {
	trie := New()
	trie.Put("account.balance", []byte("100"))
	trie.Put("account.nonce", []byte("5"))
	trie.Put("axe", []byte("tool")) // 与 "account" 共享前缀 "a"

	root, ok := trie.root.(*Extension)
	if !ok {
		t.Fatalf("根应为 Extension（共享前缀 'a'），实际 %T", trie.root)
	}
	if got := nibblesToKey(root.Path); got != "a" {
		t.Fatalf("根 Extension 路径应为 'a'，实际 %q", got)
	}
	if v, ok := trie.Get("axe"); !ok || string(v) != "tool" {
		t.Fatalf("Get(axe) = %q, %v", v, ok)
	}
	if v, ok := trie.Get("account.balance"); !ok || string(v) != "100" {
		t.Fatalf("Get(account.balance) = %q, %v", v, ok)
	}
}

// TestRootHashCascade 验证任何节点变更均级联更新根哈希。
func TestRootHashCascade(t *testing.T) {
	trie := buildTrie(t)
	h1 := trie.RootHash()
	trie.Put("account.balance", []byte("101")) // 仅改一个叶子
	h2 := trie.RootHash()
	if bytes.Equal(h1, h2) {
		t.Fatal("叶子变更后根哈希必须级联变化")
	}
	trie.Put("account.balance", []byte("100")) // 改回原值
	if !bytes.Equal(trie.RootHash(), h1) {
		t.Fatal("内容恢复后根哈希应复原（哈希由内容唯一决定）")
	}
}

func TestPrefixSearch(t *testing.T) {
	trie := buildTrie(t)
	hits := trie.PrefixSearch("account.storage")
	if len(hits) != 2 {
		t.Fatalf("前缀 account.storage 应命中 2 个键，实际 %d: %v", len(hits), hits)
	}
	if string(hits["account.storage.code"]) != "0x6080" {
		t.Fatalf("命中值错误: %v", hits)
	}
	all := trie.PrefixSearch("account.")
	if len(all) != 4 {
		t.Fatalf("前缀 account. 应命中 4 个键，实际 %d", len(all))
	}
	if none := trie.PrefixSearch("zzz"); none != nil {
		t.Fatalf("不存在的前缀应返回 nil，实际 %v", none)
	}
}

func TestRangeSummary(t *testing.T) {
	trie := buildTrie(t)
	min, max, count := trie.RangeSummary()
	if min != "account.balance" || max != "profile.name" || count != 5 {
		t.Fatalf("整树范围摘要错误: min=%q max=%q count=%d", min, max, count)
	}
	pmin, pmax, pcount, ok := trie.PrefixSummary("account.")
	if !ok || pmin != "account.balance" || pmax != "account.storage.slot0" || pcount != 4 {
		t.Fatalf("前缀范围摘要错误: min=%q max=%q count=%d ok=%v", pmin, pmax, pcount, ok)
	}
}

// TestProofVerify 验证 'account.balance' 的根到叶完整证明。
func TestProofVerify(t *testing.T) {
	trie := buildTrie(t)
	proof, ok := trie.Prove("account.balance")
	if !ok {
		t.Fatal("Prove 应确认键存在")
	}
	if len(proof) < 3 {
		t.Fatalf("证明应包含根到叶的完整节点序列（>=3 级），实际 %d 级", len(proof))
	}
	if proof[0].Kind != kindBranch {
		t.Fatalf("证明首级应为 Branch（'a'/'p' 在首 nibble 分叉），实际 %s", proof[0].Kind)
	}
	hasExt := false
	for _, e := range proof {
		if e.Kind == kindExtension {
			hasExt = true
		}
	}
	if !hasExt {
		t.Fatal("证明中应包含压缩公共前缀的 Extension 节点")
	}
	if proof[len(proof)-1].Kind != kindLeaf {
		t.Fatalf("证明末级应为 Leaf，实际 %s", proof[len(proof)-1].Kind)
	}
	if !VerifyProof(trie.RootHash(), "account.balance", []byte("100"), proof) {
		t.Fatal("合法证明应通过验证")
	}
}

// TestProofTamperDetected 验证任意篡改父级哈希均可被识破。
func TestProofTamperDetected(t *testing.T) {
	trie := buildTrie(t)
	root := trie.RootHash()
	proof, _ := trie.Prove("account.balance")
	value := []byte("100")

	// 1. 篡改父级（Extension）的子哈希引用。
	tamperedExt := append([]ProofElem{}, proof...)
	for i := range tamperedExt {
		if tamperedExt[i].Kind == kindExtension {
			tamperedExt[i].Digest = append([]byte{}, tamperedExt[i].Digest...)
			tamperedExt[i].Digest[0] ^= 0xff
			break
		}
	}
	if VerifyProof(root, "account.balance", value, tamperedExt) {
		t.Fatal("篡改 Extension 子哈希不应通过验证")
	}

	// 2. 篡改父级（Branch）的子哈希引用。
	tamperedBranch := append([]ProofElem{}, proof...)
	for i := range tamperedBranch {
		if tamperedBranch[i].Kind == kindBranch {
			for j := range tamperedBranch[i].Children {
				if tamperedBranch[i].Children[j] != nil {
					tamperedBranch[i].Children[j] = append([]byte{}, tamperedBranch[i].Children[j]...)
					tamperedBranch[i].Children[j][0] ^= 0xff
					break
				}
			}
			break
		}
	}
	if VerifyProof(root, "account.balance", value, tamperedBranch) {
		t.Fatal("篡改 Branch 子哈希不应通过验证")
	}

	// 3. 伪造叶子值。
	forged := append([]ProofElem{}, proof...)
	forged[len(forged)-1].Value = []byte("999999")
	if VerifyProof(root, "account.balance", []byte("999999"), forged) {
		t.Fatal("伪造叶子值不应通过验证")
	}

	// 4. 篡改范围摘要。
	forgedRange := append([]ProofElem{}, proof...)
	forgedRange[0].MaxKey = "zzzz"
	if VerifyProof(root, "account.balance", value, forgedRange) {
		t.Fatal("篡改范围摘要不应通过验证")
	}

	// 5. 错误的期望值 / 错误的根哈希。
	if VerifyProof(root, "account.balance", []byte("wrong"), proof) {
		t.Fatal("错误期望值不应通过验证")
	}
	badRoot := append([]byte{}, root...)
	badRoot[0] ^= 0xff
	if VerifyProof(badRoot, "account.balance", value, proof) {
		t.Fatal("错误根哈希不应通过验证")
	}
}

// TestProofForAbsentKey 验证不存在键的证明无法通过验证。
func TestProofForAbsentKey(t *testing.T) {
	trie := buildTrie(t)
	proof, ok := trie.Prove("account.missing")
	if ok {
		t.Fatal("不存在的键 Prove 应返回 false")
	}
	if VerifyProof(trie.RootHash(), "account.missing", []byte("x"), proof) {
		t.Fatal("不存在键的证明不应通过验证")
	}
}

// TestCompactEncoding 验证 Hex-Prefix 编解码往返一致。
func TestCompactEncoding(t *testing.T) {
	for _, nib := range [][]byte{
		{},
		{1},
		{1, 2},
		{0, 1, 2, 3, 4},
		{15, 14, 13, 12, 11, 10},
	} {
		for _, leaf := range []bool{false, true} {
			got, gotLeaf := compactDecode(compactEncode(nib, leaf))
			if gotLeaf != leaf || !equalNib(got, nib) {
				t.Fatalf("编解码往返失败: nib=%v leaf=%v -> %v %v", nib, leaf, got, gotLeaf)
			}
		}
	}
}
