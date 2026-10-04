# backupcut — 分区备份一致性截点检查器

每条记录携带提交版本（commit version），跨分区事务携带参与者清单。
给定一个目标截点（每个分区一个版本前沿），检查器验证该截点是否可恢复：
**每个跨分区事务要么在所有参与者处完整包含，要么完整排除**——绝不允许
各分区独立取最新版本。

## 模型

- `Partition`：有序条目日志，条目为 `data` / `commit` / `abort`，均带版本号。
- `Txn`：事务 ID + 参与者分区清单（单分区事务不产生跨区约束）。
- `Cut`：每个分区一个版本前沿，`version <= cut[p]` 的条目进入备份。
- 事务在某参与者处"被包含" ⟺ 该参与者日志中其 commit 标记版本 ≤ 前沿。

边界情况：

- **迟到提交标记**：commit marker 版本晚于数据版本、超出前沿 → 该参与者未包含。
- **已收到数据但事务未决**：有 data 条目但无 commit/abort 标记 → 视为未包含，
  并作为"缺失参与者"证据输出（含已收到的数据版本列表）。
- **abort**：任一参与者中止则事务永远不可能完整包含，其余参与者前沿必须回退。

## 算法

`FindRecoverableCut(target)` 计算不超过 target 的**最大一致截点**：
反复扫描跨分区事务，凡部分包含者，把已包含参与者的前沿回退到其
commit 版本之下（一次回退可能级联暴露其他事务的部分包含，故迭代至不动点）。
一致截点集合对分量取 max 封闭，因此最大一致截点唯一，算法必然收敛到它。

每次回退都记录证据（`Lowering`）：哪个事务、哪些参与者缺失
（迟到 commit / pending / abort）、哪些参与者前沿被迫回退。

## 验证

`checker/checker_test.go`：

- `TestEnumerateSmallTxnSets`：随机生成小事务集合（2–3 分区、1–4 事务、
  commit/pending/abort 混合），对随机目标截点**枚举全部截点向量**暴力求
  最大一致截点，与算法结果逐一比对（300 组日志 × 5 个目标）。
- 针对性测试：迟到 commit 标记、pending 数据、abort、级联回退、
  单分区事务不受约束、完整包含事务不影响截点、日志结构校验。

## 使用

```sh
go test ./checker/          # 枚举验证
go run ./cmd/cutcheck       # 内置演示场景
go run ./cmd/cutcheck -scenario scenario.json
```

场景 JSON 格式见 `cmd/cutcheck/main.go` 中 `scenario` 结构：
`partitions`、`txns`（含参与者清单）、`entries`（data/commit/abort + 版本）、
可选 `target`（缺省取各分区最新版本）。
