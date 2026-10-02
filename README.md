# mpsm — 多分区确定性事务状态机

基于本地确定性日志的多分区键值状态机（Go 标准库，无第三方依赖）。

## 设计

- **确定性日志**：每条事件（prepare/commit/abort）由定序器分配单调 LSN。
  所有副本严格按 LSN 顺序应用，因此状态收敛与网络到达顺序无关。
- **乱序吸收**：`Deliver` 前有界重排缓冲（`ReorderWindow`），乱序包按 LSN
  重排后应用；已应用的 LSN 重复投递为幂等空操作。
- **两阶段提交回放**：`Replay` 可从日志任意后缀重放 prepare/commit/abort
  事件，重建事务状态。
- **依赖拓扑排序**：已提交事务若与仍处 prepared 的事务冲突则阻塞等待；
  就绪集合按冲突依赖图做 Kahn 拓扑排序，决胜序为 `(commitLSN, txnID)`，
  完全确定性。
- **可审计决胜证据**：并发事务在同一决胜批次内写同一键时，发出
  `Evidence`（JSON 可序列化）：键、胜/负事务、双方 commit LSN、规则与原因。
- **内存有界**：`MaxPending`（在途事务上限，背压报错）、`ReorderWindow`
  （乱序窗口上限）、快照后 `Compact` 截断日志。
- **崩溃恢复**：`Snapshot` 捕获切片点（分区数据 + 在途事务 + 证据 +
  下一期望 LSN），`Restore` 后重放日志后缀即可精确恢复，支持快照时
  存在两阶段进行到一半的事务。

## 运行

```sh
go test ./...      # 测试（含验收边界）
go run ./cmd/demo  # 运行样例
```

## 验收边界对应测试

- `TestOutOfOrderDeterminism`：T1/T2 跨 2 分区并发交错，注入 50 种
  乱序排列，最终键值状态与审计证据完全一致。
- `TestSnapshotReplayConsistency`：在每个可能的切片点生成快照，恢复后
  乱序重放剩余日志，状态与证据和全量顺序回放完全一致。
