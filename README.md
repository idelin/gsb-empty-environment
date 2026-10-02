# cdc — 内容定义分块（Rolling Hash CDC）+ 默克尔树

基于 **Buzhash 滚动哈希** 的可验证内容分块器，纯 Go 标准库实现。

## 原理

- **滚动哈希**：Buzhash 在 48 字节滑动窗口上以 O(1) 滚动更新 32 位哈希；
  替换表由 SHA-256 计数器模式从固定种子确定性展开，无需硬编码随机表。
  窗口填满后哈希只取决于最近 48 字节，与分块起点无关——这是编辑后
  边界能够重新同步的关键（填充阶段不施加驱逐项）。
- **分块边界**：当滚动哈希低 13 位全零（期望概率 1/8192）且当前分块
  ≥ minSize 时切分；超过 maxSize 强制切分。默认 min/avg/max =
  4 KiB / 8 KiB / 64 KiB。边界只由局部内容决定，因此在文件中间插入
  字节只会重新切分邻近的 1–2 个分块，其余分块边界与哈希完全不变。
- **默克尔树**：叶子为各分块的 SHA-256，内部节点为子节点拼接的
  SHA-256；奇数节点自我复制提升。支持生成/验证认证路径（Proof），
  增量同步时只需传输变化的分块及其默克尔路径。

## 文件

- `buzhash.go` — Buzhash 滚动哈希
- `chunker.go` — 内容定义分块器（`Chunker.Split`）
- `merkle.go` — 默克尔树、认证路径、`Diff` 增量对比
- `chunker_test.go` / `merkle_test.go` — 测试（含 10 MiB 验收用例）
- `cmd/demo/main.go` — 端到端演示

## 运行

```sh
go test ./... -v     # 运行测试
go run ./cmd/demo    # 运行演示
```

## 验收结果（10 MiB 文件中间插入 100 字节）

`go run ./cmd/demo` 输出：

```
file: 10485760 bytes -> 10485860 bytes (inserted 100 bytes at offset 5242880)
chunks: 852 -> 852
old merkle root: 48d0c1d3e6153f9d4a27b89930db71a7cec8b3f9765c3d5fddcad20f27d5691b
new merkle root: 3aec1c295ed1dc2fb2fccc47f33bba4215843e4059ec5db76f405a733efa9f62
reused: 10460446 / 10485860 bytes (99.7576%)
incremental transfer: 1 chunk(s)
  chunk #424 offset=5224016 len=25414 hash=b9241b913dbc1f5b... merkle-path=10 hashes verified=true
```

`go test ./... -v` 结果（全部通过）：

```
--- PASS: TestSplitCoversInputExactly      分块无缝覆盖整个输入
--- PASS: TestChunkSizeBounds              min/max 尺寸约束生效
--- PASS: TestSplitDeterministic           多次切分结果一致
--- PASS: TestInsertMiddleLocalizesChanges 验收：99.7576% 哈希复用，
                                           仅 1 个分块变化且位于插入点附近，
                                           其默克尔路径验证通过
--- PASS: TestMerkleRootStable             根哈希稳定且对篡改敏感
--- PASS: TestMerkleProofAllLeaves         全部叶子的认证路径可验证
--- PASS: TestMerkleProofDetectsTampering  篡改叶子无法通过验证
--- PASS: TestEmptyTree                    空输入边界行为
```
