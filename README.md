# multipaxos — Multi-Paxos 多数派共识与无空洞日志提交引擎 + 形式化约束巡检仪

纯 Go 标准库实现（无第三方依赖），包含：

- **两阶段共识**：Phase 1 `Prepare/Promise` 与 Phase 2 `Accept/Accepted`。
- **领导者租约**：租约有效期内跳过 Phase 1，连续 `LogEntry` 直接经 Phase 2 提交。
- **无空洞提交**：新 Leader 在竞选成功的 Phase 1 中收集多数派的全部已接受条目，
  对 `[1, maxSlot]` 逐一重放 Phase 2 —— 空洞填 `(no-op)`，已被看到的值按最高 ballot 保留，
  少数派脏日志在提交前缀广播时被安全覆写；已提交条目不可变。
- **离线完整性巡检仪（Inspector）**：对全量状态快照做形式化不变式断言，主动发现
  悬挂引用、孤立环路、数据倾斜等异常。

## 不变式（巡检项）

| Kind | 含义 |
|---|---|
| `CommittedDivergence` | 同一槽位在不同副本上提交了不同的值（核心安全性被破坏） |
| `CommitGap` | 已提交集合不是连续前缀 / commitIndex 与实际不一致 / 已提交槽位缺日志 |
| `DanglingRef` | 已提交条目引用了未提交（或不存在）的槽位 —— 悬挂引用 |
| `FutureRef` | 条目引用自身或未来槽位 |
| `RefCycle` | 已提交引用图存在孤立环路 |
| `BallotAnomaly` | 已接受条目的 ballot 超过本节点承诺的 ballot |
| `MinorityCommit` | 标记为已提交的值实际存储节点数不足多数派 |
| `DataSkew` | 副本日志长度 / commitIndex 落后集群超过容忍度 —— 数据倾斜 |

## 运行样例

```sh
go run ./cmd/demo
```

演示 5 节点集群：分区 `{1,2}|{3,4,5}` → 旧 Leader 少数派脏写 → 多数派选新 Leader →
Phase 1 补全未提交槽位（no-op 填洞）→ 愈合后安全覆写少数派脏日志 → 巡检仪全量断言。

## 测试

```sh
go test -race ./...
```

覆盖：基础共识、租约跳过 Phase 1、少数派无法当选、**5 节点分区验收场景**
（`TestPartitionAcceptance`）、空洞修补与已选值保留、已提交条目不可变、
并发提案 + 并发巡检（`-race`）、严格无 goroutine 泄漏，以及巡检仪全部
边界条件（分歧/空洞/悬挂引用/未来引用/环路/ballot 异常/少数派提交/数据倾斜/确定性输出）。

测试结果（Go 1.11，`go test -race`）：17/17 PASS。
