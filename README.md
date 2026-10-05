# readbarrier — 确定性读屏障协调器

报表类读取要求多个分片在**同一个逻辑提交点**上的一致性视图：绝不能把分片 A
的新值和分片 B 的旧值拼在一起。本仓库用 Go 实现了一个确定性读屏障协调器：
只有当所有所需分片的**连续**已应用进度都达到目标提交号时，才发放固定在该
提交号的只读视图。

## 语义

- **连续进度**：分片上报"已连续应用到提交号 N"。乱序到达的高号报告会被缓
  冲，中间缺口补齐前不计入进度（`drain` 只按 watermark+1 逐个推进）。
- **读屏障**：`Wait(target, shards, deadline)` 注册请求。所有所需分片在线、
  已恢复且连续水位 ≥ target 时发放 `View{Commit: target}`——视图固定在目
  标号，即使分片已经走得更远。
- **超时**：`JudgeTimeouts` 在截止 tick 判定失败，返回**每个**落后分片及其
  缺口（未知进度标记 `Known=false`），绝不返回伪成功的部分结果。
- **同时刻顺序**：先应用进度事件，再判超时。进度与截止落在同一 tick 时，进
  度获胜（`TestProgressAtDeadlineTickWins`）。
- **幂等**：重复报告、重复恢复证明不改变状态、不产生额外结果。
- **epoch 与重启**：报告/证明携带 epoch。旧 epoch 报告被丢弃；新 epoch 出现
  时进度重置为未知，直到该实例的恢复证明（`Recover`）到达；证明前到达的报
  告被缓冲，证明落地后按连续性冲刷。
- **共享等待索引**：`index[shard] -> {reqID}`，分片推进只触达相关请求；
  `Cancel` 只移除本请求的索引项；`Offline` 立即使等待该分片的请求失败，已
  结束的请求早已从索引移除，不会被下线分片无限占用。

## 结构

- `barrier/types.go` — 基础类型、单调测试时钟 `ManualClock`（回退即 panic）。
- `barrier/coordinator.go` — 带共享等待索引的协调器。
- `barrier/reference.go` — 简单全扫描参考实现（无索引，每个事件全量扫描所
  有请求），用于交叉校验。
- `barrier/coordinator_test.go` — 确定性单元测试：乱序应用、缺口补齐、同
  tick 截止、幂等、重启 epoch、取消、下线。
- `barrier/reference_test.go` — 固定种子随机回放（60 tick：乱序报告、缺口、
  重启、证明、下线、取消、截止判定），逐 tick 断言协调器与参考实现结果、
  资源计数完全一致。

## 状态资源计数

`Stats()` 暴露：`Shards`（跟踪的分片数）、`PendingRequests`（待决请求）、
`IndexEntries`（等待索引项）、`BufferedReports`（乱序缓冲报告）。测试断言所
有请求结束后前三项中与请求相关的计数归零、缺口补齐后缓冲排空，证明无状态
泄漏。

## 运行

```sh
GO111MODULE=on go test ./...        # 或 go test ./... -v
```

## 测试结果

```
=== RUN   TestGapBlocksProgressUntilFilled          --- PASS
=== RUN   TestViewPinnedAtTarget                    --- PASS
=== RUN   TestTimeoutReportsAllLaggingShards        --- PASS
=== RUN   TestProgressAtDeadlineTickWins            --- PASS
=== RUN   TestDeadlineWithoutProgressTimesOut       --- PASS
=== RUN   TestDuplicateEventsAreIdempotent          --- PASS
=== RUN   TestRestartEpochAndRecoveryProof          --- PASS
=== RUN   TestSharedIndexAndCancel                  --- PASS
=== RUN   TestOfflineFailsWaitersAndReleasesState   --- PASS
=== RUN   TestCrossCheckReference                   --- PASS
    cross-check replay finished: 60 ticks, coordinator == reference
PASS
ok      readbarrier/barrier
```
