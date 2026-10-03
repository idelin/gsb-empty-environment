# merklediff — 可验证日志快照与差异回溯比对器

通过默克尔树高效识别两个日志快照版本之间的差异记录。

## 原理

- 记录按序组织为 **16 叉默克尔树**：叶子为记录哈希（SHA-256），
  内部节点为 16 个子节点哈希拼接后的 SHA-256。
- **全等判定**：仅比较两个版本的根哈希，相同即秒级判定全等，展开 0 个节点。
- **差异回溯**：根不同则自顶向下下钻，只展开哈希不一致的分支，
  哈希相同的子树整体跳过，最终仅输出变动的叶子列表。
- 16 叉树下 100 万条记录仅 5 层，单条变更最多展开 5 个内部节点。

## 结构

- `merkle/merkle.go` — 树的构建（`Build`）与差异比对（`Diff`），仅用标准库。
- `merkle/merkle_test.go` — 验收测试（`go test`）。
- `cmd/merklediff/main.go` — 运行样例。

## 运行

```sh
go test ./merkle/ -v     # 运行测试
go run ./cmd/merklediff  # 运行样例
```

## 验收结果

两棵 100 万条记录的树、仅 3 条被修改：

- 全等判定（根哈希比对）：约 1µs，展开 0 个节点。
- 差异回溯：**展开 13 个节点**（远小于 50），约 20µs，
  精准定位 3 条变动明细，免去全量扫描。

测试输出：

```
--- PASS: TestEqualTreesInstant
--- PASS: TestMillionRecordsThreeChanges
    merkle_test.go:75: 100 万条记录 / 3 条变更: 展开 13 个节点即定位全部变动
--- PASS: TestDiffVariousPositions
--- PASS: TestDiffAppendedRecords
--- PASS: TestDiffAllChanged
```
