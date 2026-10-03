# multipaxos

Multi-Paxos 多数派共识与无空洞日志提交引擎 —— 基础协议状态机与核心语义实现(Go,仅标准库)。

## 设计

- **两阶段协议**:Phase 1(`Prepare`/`Promise`)选主并收集多数派日志;Phase 2(`Accept`/`Accepted`)复制日志条目。
- **领导者租约快速路径**:租约有效的领导者跳过 Phase 1,直接以 Phase 2 连续提交 `LogEntry`;租约由多数派的 `Accepted`/心跳应答续期,失去多数派联系即在租约到期时退位。
- **空洞修补**:新领导者当选后对多数派报告的每个槽位重跑 Phase 2——取最高 ballot 的已接受值;无人持有的槽位填 no-op;少数派脏日志(从未被多数派选定)被安全覆写。跟随者侧的空洞/脏槽位由心跳携带的已提交前缀修复。
- **无空洞提交**:`commitIndex` 只沿连续已选定(chosen)前缀推进,绝不跳过空洞。
- **单节点确定性时钟**:节点不读墙钟、不启 goroutine、不做 I/O;时间仅由 `OnTick` 推进,消息仅经由 `Cluster.Tick` 投递(下一 tick 送达),整个执行可复现。每节点一把互斥锁,支持客户端并发 `Propose` 与时钟驱动并发(`go test -race` 验证)。

## 核心不变量

1. **多数派相交**:任意两个多数派必有交集 ⇒ 已被多数派选定的值,必然出现在下一任领导者 Phase 1 收集的结果中并被重新提出(不会丢、不会被 no-op 覆盖)。
2. **Promise 单调**:节点承诺 ballot `b` 后,拒绝一切更小 ballot 的 `Prepare`/`Accept`,并以 nack 告知对方当前 promised。
3. **提交前缀不可变**:`commitIndex` 单调不减,已提交前缀的值永不改变。
4. **无空洞**:任意节点 `log[:commitIndex]` 无空槽,且各节点已提交前缀逐槽一致。
5. **拓扑约束**:少数派中的旧领导者租约到期必须退位;只有多数派能选主并推进提交。

## 消息与状态

- 消息:`Prepare` / `Promise`(携带完整日志供修复)/ `Accept` / `Accepted`(slot=-1 时为租约应答)/ `Heartbeat`(携带已提交前缀)。
- 角色:`Follower` → `Candidate` → `Leader`;收到更高 ballot 即退位;选举超时采用确定性错峰 + 指数退避,抑制分区侧的 ballot 膨胀。

## 目录

- `paxos/paxos.go` — 协议状态机(Ballot/Slot/Message/Node,全部状态转换)
- `paxos/cluster.go` — 确定性传输与时钟 harness(消息队列、分区/断链模拟)
- `paxos/paxos_test.go` — 状态机边界条件、租约、空洞修复、并发与泄漏测试
- `paxos/scenario_test.go` — 5 节点网络分区验收场景
- `cmd/demo/main.go` — 端到端运行样例

## 运行

```sh
go test ./... -race -v   # 测试
go run ./cmd/demo        # 运行样例
```

## 运行样例输出

```
== Phase 1: elect a leader and commit v0 on all 5 nodes ==
leader elected: node0 (ballot {1 0})
  node0 Leader   promised={1 0} commitIndex=1 committed=["v0"] logLen=1
  ...全部 5 节点提交 ["v0"]...

== Phase 2: partition {0,1} | {2,3,4}; old leader writes dirty entries ==
  node0 Leader   commitIndex=1 committed=["v0"] logLen=3   <- dirty1/dirty2 仅在少数派,未提交
  node1 Follower commitIndex=1 committed=["v0"] logLen=3
  node2..4       commitIndex=1 committed=["v0"] logLen=1

== Phase 3: majority elects node2; phase-1 repair; commits v1,v2 ==
new leader: node2 (ballot {2 2})
  node2 Leader commitIndex=3 committed=["v0" "v1" "v2"]    <- 多数派推进,少数派停在 1

== Phase 4: heal; committed prefix overwrites the dirty minority log ==
  全部 5 节点 commitIndex=4 committed=["v0" "v1" "v2" "post"]

== Verification ==
OK: all 5 nodes share the gap-free committed prefix [v0 v1 v2 post]; dirty entries overwritten
```

## 测试结果

`go test ./... -race -v`(Go 1.24.2)—— 14 个测试全部通过:

| 测试 | 覆盖 |
|---|---|
| TestBallotOrdering | ballot 全序与零值 |
| TestPreparePromiseAndRejection | 低 ballot Prepare 被拒/nack,高 ballot 被承诺 |
| TestPromiseCarriesLogForRepair | Promise 携带含空洞的完整日志 |
| TestAcceptRejectAndDirtyOverwrite | 低 ballot Accept 拒绝;高 ballot 覆写脏槽位 |
| TestFollowerCommitIsGapFree | 空洞存在时 commitIndex 不跳跃;心跳前缀修补空洞;过期心跳无效 |
| TestElectionAndBasicCommit | 5 节点选主 + 连续提交 + 全员一致 |
| TestLeaseBypassesPhase1 | 租约内连续提案零新增 Prepare(绕过 Phase 1) |
| TestLeaseExpiryStepsDown | 孤立领导者租约到期退位;多数派另选新主 |
| TestHoleRepairAndHighestBallotWins | 新主 Phase 1 修复:最高 ballot 值胜出、空洞填 no-op、全员收敛 |
| TestConcurrentProposeAndTick | 8 goroutine 并发 Propose + 时钟驱动,-race 无数据竞争 |
| TestNoGoroutineLeak | 库自身零 goroutine 泄漏(NumGoroutine 前后一致) |
| TestPartitionNewLeaderRepairsAndOverwrites | **验收场景**:分区→旧主脏日志→多数派新主修复→愈合后安全覆写 |
| TestMajorityAcceptedValueSurvives | 已被多数派接受但未提交的值必须存活到新任期 |
| TestOldLeaderCannotCommitInMinority | 少数派旧主无法提交,租约到期后拒绝提案 |

```
ok  	multipaxos/paxos	1.153s
```
