# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 标准库）

多节点并发竞争与冲突仲裁器：无单点故障的分布式共识日志协议实现。
仅使用 Go 标准库，传输层为同步内存网络（可模拟网络分区），整个引擎
不启动任何后台 goroutine，因此天然无泄漏、无死锁。

## 协议要点

- **两阶段共识**：`Prepare/Promise`（Phase 1）+ `Accept/Accepted`（Phase 2），
  外加 `Commit` 学习阶段把已提交条目教给不在接受法定人数内的跟随者。
- **领导者租约**：Phase 1 获胜后获得租约，租约内直接走 Phase 2 提交连续
  `LogEntry`，跳过 Phase 1（由 `TestLeaseSkipsPhaseOne` 用消息计数断言）。
- **空洞修补**：新 Leader 在 Phase 1 收集法定人数的已接受条目，对每个槽位
  重提最高 ballot 的值，空洞槽位填 no-op，保证已提交前缀无空洞。
- **冲突仲裁**：Ballot 为 `(Num, NodeID)` 全序对；接受方拒绝更低 ballot；
  被拒绝的回复携带对方已承诺 ballot，提议方据此抬升编号并重试——
  分区期间被抬高的 promise 不会永久阻塞新 Leader。
- **脏日志覆写**：只在少数派上 accepted（未提交）的条目可被新 Leader 安全
  覆写；已提交（多数派 accepted）的值在 repair 中必然被选中并保留。

## 并发与一致性保障

- 每个节点一把 `mu` 保护全部状态；持有锁期间绝不发起网络调用，锁图无环。
- `proposeMu` 串行化本节点的客户端提案，槽位分配原子完成。
- `Propose` 只有在多数派接受后才返回（线性一致），且对同一值幂等。
- `commitIndex` 只按连续已提交槽位推进，提交前缀永远无空洞。

## 目录结构

- `paxos/` — 共识引擎（ballot / entry / message / network / node）
- `paxos/paxos_test.go` — 单元测试：ballot 全序、Promise/Accept 拒绝、租约跳过
  Phase 1、租约过期、空洞修补、Commit 教学
- `paxos/concurrency_test.go` — 25 goroutine x 5 节点并发提案一致性、
  单节点并发串行化、goroutine 泄漏断言
- `paxos/partition_test.go` — 验收场景：5 节点分区、旧 Leader 被 fencing、
  新 Leader Phase 1 补全槽位并安全覆写少数派脏日志
- `cmd/demo/` — 可运行样例，完整演示分区-换主-修复-收敛流程

## 运行

```sh
go test ./paxos/ -race -v -count=1   # 全部测试（含竞态检测）
go run ./cmd/demo                     # 运行样例
```

## 测试结果（go1.24.2, -race）

```
--- PASS: TestConcurrentProposersConsistency
--- PASS: TestConcurrentSameNodeProposals
--- PASS: TestNoGoroutineLeak
--- PASS: TestPartitionLeaderReplacement
--- PASS: TestCommittedValueSurvivesLeaderChange
--- PASS: TestBallotOrdering
--- PASS: TestSingleProposalCommitsOnQuorum
--- PASS: TestPromiseRejectsLowerOrEqualBallot
--- PASS: TestAcceptRejectsBelowPromisedBallot
--- PASS: TestLeaseSkipsPhaseOne
--- PASS: TestLeaseExpiryTriggersPhaseOne
--- PASS: TestHoleFillingByNewLeader
--- PASS: TestCommitMessageTeachesMissingFollower
ok  	multipaxos/paxos
```
