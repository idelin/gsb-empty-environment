# cdc — 滚动哈希内容定义分块树

基于 **Buzhash** 滚动哈希的可验证内容定义分块器（Content-Defined Chunking），
对每个分块计算 SHA-256 并构建**默克尔树**。在数据流中动态识别分块边界；
当大文件中间插入字节时，仅影响局部 1–2 个分块，其余分块哈希全部复用。
仅使用 Go 标准库。

## 原理

- **Buzhash 滚动哈希**（`buzhash.go`）：对 48 字节滑动窗口维护
  `h = XOR rotl(table[b[i]], w-1-i)`，窗口滑动一字节只需 O(1) 更新。
- **内容定义分块**（`chunker.go`）：当滚动哈希命中 1/8192 的特征模式
  （`h & 0x1FFF == 0`）且当前分块 ≥ 2 KiB 时切分，上限 64 KiB。
  边界只取决于局部内容，因此局部编辑只引起局部重切分。
  提供批量 `Chunker.Split` 与流式 `Splitter.Write/Close` 两种接口。
- **默克尔树**（`merkle.go`）：叶子为分块 SHA-256，内部节点为子节点拼接的
  SHA-256；支持生成与验证 Merkle 证明（`Proof` / `VerifyProof`）。
- **增量对比**（`diff.go`）：`AlignDiff` 通过对齐公共前后缀找出变化的
  1–2 个分块；`ReuseRatio` 统计哈希复用率。

## 运行样例

```console
$ go run ./cmd/cdcdemo
file: 10485760 bytes -> insert 100 bytes at offset 5242880 -> 10485860 bytes
chunks: 1056 -> 1056 (min 2048 / avg 8192 / max 65536 bytes)
merkle root: b553bafe4c9db32e...
merkle root: 03f9c582c4c432fb... (after insert)
changed chunks: 1 old -> 1 new
  new chunk @5233188 len=17556 hash=1c10476a58f46b2a...
chunk hash reuse: 99.91% (1055 of 1056 new chunks unchanged)
incremental sync to new tree: 1 chunk(s) + merkle path of 11 hashes (depth 11), proof verifies: true
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestBuzhashRollingMatchesDefinition   --- PASS   # 滚动哈希与定义一致
=== RUN   TestSplitDeterministicAndComplete     --- PASS   # 分块确定性、可重组、大小约束
=== RUN   TestStreamingMatchesBatch             --- PASS   # 流式与批量切分结果一致
=== RUN   TestInsertMiddleReuse                 --- PASS   # 验收：10MiB 中间插入 100 字节
    old: 1056 chunks, new: 1056 chunks
    changed: 1 old chunk(s) -> 1 new chunk(s)
    chunk hash reuse: 99.9053%
    incremental sync: 1 chunk(s) + merkle path of 11 hashes (tree depth 11)
=== RUN   TestMerkleProof                       --- PASS   # 全部叶子证明可验证，篡改失败
=== RUN   TestMerkleEmptyAndSingle              --- PASS   # 空树/单叶边界
PASS
ok      cdc
```

验收边界：10 MiB 文件中间插入 100 字节后，分块算法动态重新切分，
**99.91%** 的分块哈希与旧树一致（≥ 99% 要求），增量传输仅需同步
受影响的 **1 个分块**及 **11 个哈希**的默克尔树路径（树深 11）。

## 用法

```go
chunks := cdc.NewChunker().Split(data)          // 内容定义分块
tree := cdc.NewMerkleTreeFromChunks(chunks)     // 构建默克尔树
root := tree.Root()
proof := tree.Proof(i)                          // 第 i 个分块的证明
ok := cdc.VerifyProof(chunks[i].Hash, i, proof, root)

oldChanged, newChanged := cdc.AlignDiff(oldChunks, newChunks) // 增量对比
reuse := cdc.ReuseRatio(oldChunks, newChunks)                 // 哈希复用率
```
