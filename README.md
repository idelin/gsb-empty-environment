# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 版）

仅使用 Go 标准库实现的 Multi-Paxos 共识日志协议，附带**形式化约束巡检与不变式断言仪**（离线完整性巡检器）。

## 特性

- **两阶段共识**：Prepare/Promise（Phase 1）+ Accept/Accepted（Phase 2），多数派（quorum）提交，无单点故障。
- **领导者租约优化**：租约有效期内，领导者对连续 LogEntry 跳过 Phase 1，直接 Phase 2 提交（`LeaseDuration` 可观测、可测试）。
- **新领导者补全（无空洞）**：`BecomeLeader` 通过一次 PrepareAll 收集多数派的已接受值，对所有槽位执行补全：已接受值按 Paxos 安全性重提，空槽填 noop，保证日志无空洞。
- **空洞修补与脏日志覆写**：心跳携带已提交日志尾部，分区愈合后落后副本自动收敛；少数派上的未提交脏日志被多数派已提交值安全覆写。
- **离线完整性巡检**（`Inspect`）：主动发现
  - 悬挂引用（commitIndex 越界、commitIndex 之下的空洞、非法/空槽 Prev 引用）
  - 孤立环路（Prev 链成环、链断裂成为孤儿）
  - 数据倾斜（已提交条目副本数低于 quorum、副本间日志长度异常倾斜）
- **不变式断言仪**（`AssertInvariants`）：ElectionSafety、LogMatching、StateMachineSafety、LeaderCompleteness。
- **严格无泄漏**：引擎为全同步设计，不启动任何后台 goroutine；测试中含 goroutine 基线断言，并以 `-race` 验证并发安全。

## 运行

```sh
go test ./... -race -count=1 -v   # 测试（含 5 节点分区验收场景）
go run ./cmd/demo                 # 运行样例：选举 -> 分区 -> 换主 -> 覆写 -> 巡检
```

## 验收场景（TestPartitionFailover）

1. 5 节点集群，node0 为 Leader，提交 x1、x2。
2. 网络分区 `{0,1} | {2,3,4}`；旧 Leader 的 `dirty` 写仅到达少数派，无法提交。
3. 多数派选举 node2 为新 Leader，Phase 1 补全槽位后提交 v3；分区期间全部不变式成立。
4. 愈合后心跳收敛少数派，槽位 3 的脏日志被已提交的 v3 安全覆写，旧 Leader 退位；集群恢复全量提交，巡检零发现。

## 结构

- `paxos/types.go` — Ballot / LogEntry / Slot / Promise 等核心类型
- `paxos/cluster.go` — 同步内存网络与分区控制（无后台 goroutine）
- `paxos/node.go` — 共识节点：两阶段、租约、补全、心跳收敛
- `paxos/inspector.go` — 离线完整性巡检 + 形式化不变式断言
- `paxos/paxos_test.go` — 共识行为与分区验收测试
- `paxos/inspector_test.go` — 巡检仪/断言仪边界条件测试
- `cmd/demo/main.go` — 端到端运行样例
