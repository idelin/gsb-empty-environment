# paxoslab — Multi-Paxos 共识日志 + 增量快照/检查点归档

纯 Go 标准库实现，无第三方依赖。

## 结构

- `paxos/` — Multi-Paxos 多数派共识与无空洞日志提交引擎
  - `types.go` — Ballot（Term, Node 全序）、Entry、两阶段消息
  - `network.go` — 同步内存传输，支持任意链路阻断与分区模拟，带消息计数器
  - `node.go` — Prepare/Promise + Accept/Accepted 两阶段；领导者租约下跳过
    Phase 1 直接 Phase 2 提交连续 LogEntry；新 Leader 通过 Phase 1 收集
    各槽位最高 ballot 的已接受值，修补空洞（无值槽位填 no-op）；已提交
    条目永不被覆写，少数派脏日志在同步时被已提交值安全替换
- `snapshot/` — 轻量级增量快照与检查点归档
  - `machine.go` — 有序 KV 状态机；增量捕获的临界区仅为一次 map swap
    （低停顿），归档失败自动回滚脏集合，不丢变更
  - `archive.go` — 不可变 Segment/Checkpoint 存储，CRC32 完整性校验；
    `MemArchive` 与 `FileArchive`（tmp+rename 原子写）两种实现
  - `snapshotter.go` — 增量捕获、全量 Checkpoint（GC 被覆盖的增量段）、
    链式恢复（缺口检测 `ErrGap`）
- `cmd/demo/` — 端到端运行样例

## 运行

```sh
go test ./... -race -count=1   # 全部测试（含竞态检测）
go run ./cmd/demo              # 5 节点分区/换主/快照恢复演示
```

## 验收场景（测试覆盖）

- `TestPartitionNewLeaderOverwritesMinorityDirtyLog` — 5 节点分区
  {1,2}|{3,4,5}：旧 Leader 在少数派的提案无法提交；多数派选出新 Leader
  经 Phase 1 补全未提交槽位；分区愈合后旧 Leader 的过期任期提案被拒绝
  并退位，少数派脏日志被已提交值覆写，5 节点日志完全一致。
- `TestHoleRepairWithNoopFill` — 槽位 4/5/6 的脏值散落在不同节点，新
  Leader 采用最高 ballot 值、真正的空洞填 no-op，提交日志连续无空洞。
- `TestLeaderLeaseSkipsPhase1` / `TestLeaseExpiry` — 租约内零 Prepare
  消息；租约过期后拒绝提案直至重新当选。
- `TestConcurrentPropose` / `TestCommittedEntryNeverOverwritten` /
  `TestNoGoroutineLeak` — 并发提交一致性、已提交值不可变、无 goroutine 泄漏。
- 快照：增量捕获/恢复、空捕获跳过、Checkpoint GC、空归档恢复、链缺口
  检测、跨 Checkpoint 段重叠恢复、不可变性、CRC 损坏检测、归档失败
  回滚、Apply 顺序强制、非空机恢复拒绝、高并发 Apply+Capture+Checkpoint
  一致性、无 goroutine/临时文件泄漏。
