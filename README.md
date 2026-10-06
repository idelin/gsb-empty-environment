# aggregator：可恢复、不重复计数的 Go 聚合器

演示“检查点 + 幂等接收”模型下的精确一次（effectively-once）聚合语义。

## 核心模型

- **检查点保存累计值与待发结果**：`Checkpoint` 同时持久化计算状态
  （每 key 累计值 `Sums`、已结算基线 `Emitted`、输入进度 `InputSeq`）
  和 outbox 式的 `Pending` 待发结果列表（含稳定 ID）。
- **发布与确认分离**：结果先结算进 `Pending` 并落盘，再经 `PublishPending`
  投递；下游确认后调用 `Ack(id)` 从 `Pending` 移除并再次落盘。
- **恢复重发同结果 ID**：结果 ID 由持久化的 `EmitSeq` 派生（`res-<n>`），
  恢复后原样重发，绝不生成新 ID，因此不会重复计数。
- **幂等接收方**：`Receiver` 按结果 ID 去重，重复投递只确认不生效。

## 两个进度

`Aggregator.Progress()` 明确区分两条进度线：

| 进度 | 字段 | 含义 |
| --- | --- | --- |
| 计算状态恢复 | `Compute.RestoredFromCheckpoint` / `InputSeq` / `Sums` | 状态是否来自检查点、输入重放到哪、累计值还原值 |
| 效果确认 | `Effect.Emitted` / `Pending` / `Confirmed` | 已结算结果数、待确认数（含 ID 列表）、已确认数 |

## 故障点验证（aggregator_test.go）

| 故障点 | 测试 | 验证点 |
| --- | --- | --- |
| 结算落盘后、发布前崩溃 | `TestCrashAfterCheckpointBeforePublish` | 恢复后重发 Pending，接收方恰好生效一次 |
| 发布后、确认前崩溃 | `TestCrashAfterPublishBeforeAck` | 重发同 ID，接收方去重，总量不变 |
| 确认后、确认落盘前崩溃 | `TestCrashAfterAckBeforeCheckpoint` | 旧检查点仍含该结果，重发去重后重新确认 |
| 输入流中途崩溃 | `TestComputeStateRecoveryMidStream` | 累计值与输入进度精确还原，续算不丢不重 |
| 检查点写一半失败 | `TestCheckpointSaveFailureKeepsOldState` | 原子写保证旧检查点完好，可回滚重试 |
| 多轮崩溃端到端 | `TestEndToEndNoDoubleCountAcrossCrashes` | 接收方总量 == 输入总量，无重复计数 |
| 结果 ID 稳定性 | `TestResultIDStableAcrossRecovery` | 同一逻辑结果跨恢复 ID 不变 |

## 运行

```sh
go test ./... -v   # 故障点验证
go run ./cmd/demo  # 崩溃-恢复演示，打印两个进度
```
