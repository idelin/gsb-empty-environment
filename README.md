# readbarrier — 确定性读屏障协调器

报表读取多个分片在同一个逻辑提交点的状态。协调器只在**所有**所需分片的
**连续**已应用进度都达到目标提交号时，才发放固定在该目标号的只读视图；
否则等待，直到截止 tick 超时并返回每个落后分片及其缺口，绝不返回伪成功的
部分结果。

## 语义要点

- **连续进度**：分片上报已应用提交号（可乱序、可重复）。只有从连续点起
  无缺口的部分算进度；高号但中间有缺口的提交被缓冲在 `pending` 里，
  缺口补齐后一次性推进。重复上报是幂等 no-op。
- **读屏障**：`Submit(target, shards, deadline)`。全部满足才 `Granted`，
  视图固定在 `target`；超时返回 `TimedOut` + 每个落后分片的
  `ShardGap{Contig, Missing, Unknown, Offline}`。
- **先应用再判超时**：同一 tick 内先处理 `HandleReport`，再 `Advance`
  判定超时；进度与截止同时到达时进度优先。
- **epoch 与恢复证明**：`Restart(shard, epoch)` 后进度视为未知
  （`known=false`）；旧 epoch 报告被忽略；新 epoch 的普通报告先缓冲，
  恢复证明（`Proof` + `ProofContig`）到达后重放缓冲并恢复推进。
- **共享等待索引**：所有客户端共用一个 `shard -> requests` 索引；
  `Cancel` 只移除本请求的条目；请求结束（发放/超时/取消）立即离开索引，
  分片下线不会占用已结束请求的资源。
- **单调测试时钟**：`TestClock.Advance` 拒绝回拨（panic），测试用它
  确定性回放乱序、缺口、同时刻截止、取消与重启。

## 文件

- `barrier/types.go` — 报告、请求、结果（视图 / 落后分片缺口）类型
- `barrier/clock.go` — 单调测试时钟
- `barrier/coordinator.go` — 索引化协调器（增量维护连续进度 + 等待索引）
- `barrier/reference.go` — 全扫描参考实现：保存原始事件日志，每次评估
  全量重放并扫描整个已应用集合求连续进度，用于对拍
- `barrier/coordinator_test.go` — 回放测试

## 状态资源计数

`Coordinator.Stats()`：

| 字段 | 含义 |
|---|---|
| `PendingRequests` | 等待中的请求数 |
| `TrackedShards` | 保留的分片状态数 |
| `WaitIndexEntries` | 共享等待索引条目数（shard→req 边） |
| `PendingHoles` | 缺口上方缓冲的已应用提交数 |
| `PreProofBuffered` | 恢复证明到达前缓冲的提交数 |

请求结束即清零其索引条目；测试断言发放/超时/取消后无泄漏。

## 测试与结果

```sh
go test ./... -v
```

```
--- PASS: TestOutOfOrderGapFill              乱序应用、缺口阻塞、补齐后发放、重复幂等
--- PASS: TestProgressBeforeTimeoutAtSameTick 截止 tick 进度先到：先应用再判超时
--- PASS: TestTimeoutReportsLagging          超时返回落后分片与缺口，无部分视图
--- PASS: TestCancelRemovesOnlyOwnRequest    共享索引下取消只移除本请求
--- PASS: TestRestartEpochAndRecoveryProof   旧 epoch 忽略、证明前未知、证明后重放
--- PASS: TestOfflineDoesNotHoldFinishedRequests 下线不占用已结束请求
--- PASS: TestReferenceCrossCheck            80 tick 确定性随机回放，与全扫描参考逐条对拍
    final stats: {PendingRequests:0 TrackedShards:4 WaitIndexEntries:0 PendingHoles:49 PreProofBuffered:0}
PASS
ok  readbarrier/barrier
```
