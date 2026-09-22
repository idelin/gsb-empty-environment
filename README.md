# mvr — 多副本多值寄存器（Multi-Value Register）

纯 Go 标准库实现。多副本多值寄存器 + 版本向量 + 显式解决操作，
通信、故障与时间全部由单进程离散事件模拟器控制。

## 设计

- **版本向量（`VV`）**：`Compare` 给出 happens-before 偏序的四种关系
  （equal / before / after / concurrent），据此区分*因果覆盖*与*并发冲突*。
- **多值保留**：每个副本保存一个条目集合 `Entry{Value, Ver, Wall, Origin}`。
  并发写（版本向量不可比较）全部保留；读返回整个冲突集合。
- **后继写覆盖已观察版本**：`Write` 用本地知识（保留集版本的 join）自增
  本副本计数，新版本严格支配所有已观察版本，因此恰好覆盖它们、
  不触碰未观察到的并发写。
- **显式解决（`Resolve`）**：客户端读到冲突集后调用 `Resolve(value)`，
  产生一个支配整个已观察冲突集的新版本；该条目像普通写一样广播，
  收到它的副本用 `Apply`/`Merge` 淘汰被支配版本——解决结果随之传播。
  `Resolve` 只覆盖本副本观察到的版本，未观察到的并发写仍然存活。
- **墙钟不参与排序**：`Wall` 只是元数据，任何比较都不读它；
  墙钟再大也无法吞并并发值（见测试与样例）。
- **事件模拟器（`Sim`）**：单进程、确定性（种子驱动）。事件堆推进逻辑
  时钟；网络延迟随机；`Partition`/`Heal` 控制分区（丢消息）；
  `Crash`/`Recover` 控制宕机；每个节点可设墙钟偏移。
  写时广播是快速路径，`Sync`（反熵全状态交换）修复丢失的消息。

## 安全性证据

条目集合在"淘汰被严格支配者"的 join 下构成 join-semilattice（幂等、
交换、结合），因此只要每个写最终送达每个副本，各副本必然收敛到同一
集合——恰好是所有不被其他写因果支配的写。

- `TestRandomSafetyAndConvergence`：20 个种子 × 200 步随机操作
  （写 / 解决 / 分区 / 愈合 / 宕机 / 恢复 / 反熵，节点带随机时钟偏移），
  愈合并做一轮反熵后，每个副本的条目集与按 happens-before 算出的
  oracle（所有不被支配的写）**逐一相等**：并发值零丢失、被支配值零复活。
- `TestLargerWallClockCannotSwallowConcurrentValue`：并发写即使带着
  `1<<40` 的墙钟也不会吞并墙钟为 1 的并发值。
- `TestSuccessorWriteOverwritesObserved`：观察到旧值后的写恰好覆盖它。
- `TestResolveCoversOnlyObservedVersions`：解决只覆盖已观察版本，
  第三方并发写存活。
- `TestMergeIsIdempotentAndCommutative`、`TestApplyIgnoresDominatedDuplicates`：
  重复 / 乱序 / 合并不改变结果。

## 活性证据

- `TestPartitionThenHealConvergesInOneSyncRound`：分区期间两侧各自可写
  可读（可用性不被网络阻塞），愈合后**一轮**反熵即全副本收敛。
- `TestCrashRecoverConverges`：宕机期间错过的写，恢复后一轮反熵补齐。
- `TestRandomSafetyAndConvergence` 的收尾断言：任意故障历史之后，
  愈合 + 一轮 `SyncAll` 必然收敛（有界收敛轮次）。
- `TestResolvePropagatesAcrossReplicas`：解决结果经普通广播传播到所有副本。

## 运行

```sh
go test ./...     # 全部测试
go run ./cmd/demo # 运行样例
```

## 样例输出（`go run ./cmd/demo`）

```
== step 1: three concurrent writes; C has a huge wall clock ==
--- all three concurrent values retained; big wall clock swallows nothing (t=5) ---
  A: ["banana" ver={A:1} wall=0, "apple" ver={B:1} wall=0, "cherry" ver={C:1} wall=3600]
  B: ["banana" ver={A:1} wall=0, "apple" ver={B:1} wall=0, "cherry" ver={C:1} wall=3600]
  C: ["banana" ver={A:1} wall=0, "apple" ver={B:1} wall=0, "cherry" ver={C:1} wall=3600]

== step 2: B reads the conflict set and explicitly resolves ==
--- resolve result propagated to every replica (t=9) ---
  A: ["fruit-salad" ver={A:1,B:2,C:1} wall=5]
  B: ["fruit-salad" ver={A:1,B:2,C:1} wall=5]
  C: ["fruit-salad" ver={A:1,B:2,C:1} wall=5]

== step 3: partition C away; both sides keep writing ==
--- partitioned: sides diverge, no value lost (t=13) ---
  A: ["pie" ver={A:2,B:2,C:1} wall=9]
  B: ["pie" ver={A:2,B:2,C:1} wall=9]
  C: ["cake" ver={A:1,B:2,C:2} wall=3609]

== step 4: heal; one anti-entropy round converges everyone ==
--- converged with both partition-era values retained (t=18) ---
  A: ["cake" ver={A:1,B:2,C:2} wall=3609, "pie" ver={A:2,B:2,C:1} wall=9]
  B: ["cake" ver={A:1,B:2,C:2} wall=3609, "pie" ver={A:2,B:2,C:1} wall=9]
  C: ["cake" ver={A:1,B:2,C:2} wall=3609, "pie" ver={A:2,B:2,C:1} wall=9]

== step 5: A resolves the new conflict ==
--- final state (t=23) ---
  A: ["dessert" ver={A:3,B:2,C:2} wall=18]
  B: ["dessert" ver={A:3,B:2,C:2} wall=18]
  C: ["dessert" ver={A:3,B:2,C:2} wall=18]
converged: true
```

## 测试结果（`go test ./...`）

```
--- PASS: TestCompareRelations (0.00s)
--- PASS: TestConcurrentWritesAllRetained (0.00s)
--- PASS: TestSuccessorWriteOverwritesObserved (0.00s)
--- PASS: TestLargerWallClockCannotSwallowConcurrentValue (0.00s)
--- PASS: TestResolveCoversOnlyObservedVersions (0.00s)
--- PASS: TestMergeIsIdempotentAndCommutative (0.00s)
--- PASS: TestApplyIgnoresDominatedDuplicates (0.00s)
--- PASS: TestResolvePropagatesAcrossReplicas (0.00s)
--- PASS: TestPartitionThenHealConvergesInOneSyncRound (0.00s)
--- PASS: TestCrashRecoverConverges (0.00s)
--- PASS: TestAvailabilityDuringPartition (0.00s)
--- PASS: TestRandomSafetyAndConvergence (0.03s)   # 20 seeds × 200 random ops
PASS
ok  	mvr
```
