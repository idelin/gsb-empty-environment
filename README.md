# receiptlog — 事件溯源回执库

Go 回执库：接收执行器上报的 `accepted` / `running` / `succeeded` / `failed` 回执，
每条回执携带**执行代号**（generation）与**单调修订**（revision），以持久化事件日志为唯一事实来源。

## 修订规则

| 情况 | 处理 |
| --- | --- |
| 同修订、状态矛盾 | 报 `ConflictError`，拒绝且**不持久化**，状态不变 |
| 同修订、状态一致 | 幂等去重，无操作 |
| 低修订 | 仅归档（`Archived`），证据不丢失 |
| 高修订 | 推进状态；但**终态不可回运行**：`succeeded`/`failed` 之后的高修订非终态回执被拒为 `RegressionError` 并归档留证 |

## 代际替换（supersede）

- 执行代号可被显式 `Supersede(old, new)` 决议替换；只有决议提交后，新代才能推进业务。
- 决议提交前到达的新代回执被**缓冲**（持久化），决议提交后按修订序回放。
- 旧代的晚到终态回执**不能覆盖新代**，但记录为**待补偿效果**（`Compensation`）。
- 回执与决议可任意乱序；任意时刻存在**唯一当前代**（`Machine.Current`）。

## 持久化与恢复

- 每次提交追加一条 JSON 事件到 `events.log` 并 fsync。
- `Checkpoint()` 写入全量快照（含全部证据与缓冲）并截断日志。
- `Open(dir)` 恢复 = 加载最近检查点 + 回放日志尾部；崩溃后状态与全量回放一致。

## 验证（`go test ./...`）

- `TestPermutationsConverge`：合法回执流的**全排列乱序**投递，最终状态一致且证据完整。
- `TestSupersedePermutations`：回执与替换决议的 6! 种乱序组合，核对唯一当前代、
  新代仅在决议后推进、晚到成功记入待补偿、证据不丢失。
- `TestCheckpointRecovery` / `TestCheckpointTruncatesLog`：检查点 + 尾部日志恢复等价于全量回放。
- `TestConflictNotPersisted` / `TestUniqueCurrentAfterRecovery`：冲突不落地、恢复后当前代唯一。
