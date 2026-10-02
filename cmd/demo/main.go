// 演示：构建带公共前缀的 MPT，输出 'account.balance' 的根到叶证明，
// 并展示篡改父级哈希会被验证器识破。
package main

import (
	"encoding/hex"
	"fmt"
	"sort"

	"mpt"
)

func shortHash(b []byte) string {
	if len(b) == 0 {
		return "(nil)"
	}
	return hex.EncodeToString(b)[:16] + "…"
}

func main() {
	trie := mpt.New()
	kv := [][2]string{
		{"account.balance", "100"},
		{"account.nonce", "5"},
		{"account.storage.code", "0x6080"},
		{"account.storage.slot0", "0x00"},
		{"profile.name", "alice"},
	}
	fmt.Println("== 插入键值对（含公共前缀，路径自动压缩） ==")
	for _, p := range kv {
		trie.Put(p[0], []byte(p[1]))
		fmt.Printf("  PUT %-22s -> %-8s root=%s\n", p[0], p[1], shortHash(trie.RootHash()))
	}

	min, max, count := trie.RangeSummary()
	fmt.Printf("\n== 整树范围摘要 ==\n  min=%q max=%q count=%d\n", min, max, count)

	fmt.Println("\n== 前缀检索 PrefixSearch(\"account.storage\") ==")
	hits := trie.PrefixSearch("account.storage")
	keys := make([]string, 0, len(hits))
	for k := range hits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %s = %s\n", k, hits[k])
	}
	pmin, pmax, pcount, _ := trie.PrefixSummary("account.")
	fmt.Printf("前缀 \"account.\" 范围摘要: min=%q max=%q count=%d\n", pmin, pmax, pcount)

	// ---- 生成并验证 'account.balance' 的默克尔证明 ----
	const target = "account.balance"
	proof, ok := trie.Prove(target)
	fmt.Printf("\n== 键 %q 的根到叶节点序列证明（%d 级） ==\n", target, len(proof))
	for i, e := range proof {
		path, _ := mpt.DecodePath(e)
		fmt.Printf("  [%d] %-9s path=%-14s hash=%s range=[%s .. %s] count=%d\n",
			i, e.Kind, path, shortHash(e.Hash()), e.MinKey, e.MaxKey, e.Count)
	}
	fmt.Printf("  证明存在性: %v\n", ok)

	value, _ := trie.Get(target)
	fmt.Println("\n== 验证证明 ==")
	fmt.Printf("  合法证明验证: %v\n", mpt.VerifyProof(trie.RootHash(), target, value, proof))

	// ---- 篡改攻击 1：修改父级（Branch）的子哈希引用 ----
	tampered := make([]mpt.ProofElem, len(proof))
	copy(tampered, proof)
	for i := range tampered {
		if tampered[i].Kind == "branch" {
			for j := range tampered[i].Children {
				if tampered[i].Children[j] != nil {
					tampered[i].Children[j] = append([]byte{}, tampered[i].Children[j]...)
					tampered[i].Children[j][0] ^= 0xff // 篡改父级哈希引用
					break
				}
			}
			fmt.Printf("  篡改父级 [%d] branch 子哈希后验证: %v（识破）\n",
				i, mpt.VerifyProof(trie.RootHash(), target, value, tampered))
			break
		}
	}

	// ---- 篡改攻击 2：伪造叶子值 ----
	forged := make([]mpt.ProofElem, len(proof))
	copy(forged, proof)
	forged[len(forged)-1].Value = []byte("999999")
	fmt.Printf("  伪造叶子值 100->999999 后验证: %v（识破）\n",
		mpt.VerifyProof(trie.RootHash(), target, []byte("999999"), forged))

	// ---- 篡改攻击 3：篡改范围摘要 ----
	forgedRange := make([]mpt.ProofElem, len(proof))
	copy(forgedRange, proof)
	forgedRange[0].MaxKey = "zzzz"
	fmt.Printf("  篡改根节点范围摘要后验证: %v（识破）\n",
		mpt.VerifyProof(trie.RootHash(), target, value, forgedRange))

	// ---- 更新键值，根哈希级联变化 ----
	before := trie.RootHash()
	trie.Put(target, []byte("250"))
	fmt.Printf("\n== 更新 %q 100->250 ==\n  旧根哈希: %s\n  新根哈希: %s（已级联变化）\n",
		target, shortHash(before), shortHash(trie.RootHash()))
}
