# mpt — 带范围摘要的可验证前缀树（Merkle Patricia Trie）

纯 Go 标准库实现，结合字典树与前缀压缩、默克尔哈希承诺：

- **路径压缩**：`Extension`（共享路径）/ `Leaf`（剩余路径+值）/ `Branch`（16 路分叉）三类节点，
  键按 4-bit nibble 展开，采用 Hex-Prefix 紧凑编码。
- **范围摘要**：每个节点承诺子树的 `MinKey / MaxKey / Count`，并纳入节点哈希。
- **默克尔承诺**：任何节点变更沿路径级联更新根哈希；内容相同则根哈希必然相同。
- **可验证证明**：`Prove` 输出根到叶的完整节点序列，`VerifyProof` 校验哈希链接、
  路径走向、范围摘要与叶子键值——篡改任意父级哈希即断链识破。
- **前缀检索**：`PrefixSearch` / `PrefixSummary` 按键前缀快速定位子树。

## API

```go
trie := mpt.New()
trie.Put("account.balance", []byte("100"))
v, ok := trie.Get("account.balance")
root := trie.RootHash()
hits := trie.PrefixSearch("account.")
min, max, count := trie.RangeSummary()
proof, ok := trie.Prove("account.balance")
ok = mpt.VerifyProof(root, "account.balance", v, proof)
```

## 运行

```bash
go test ./... -v   # 测试
go run ./cmd/demo  # 演示：插入、前缀检索、证明生成与篡改检测
```
