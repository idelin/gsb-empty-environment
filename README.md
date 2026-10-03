# Multi-Paxos 共识日志 + 多租户 DRR 公平调度器 (Go)

纯标准库实现，无第三方依赖。包含两个独立组件：

- `paxos/`：Multi-Paxos 多数派共识与无空洞日志提交引擎
- `scheduler/`：多租户隔离与配额公平的 Deficit Round Robin 调度器
- `cmd/demo/`：端到端运行样例（网络分区 → 换主 → 修复 → 收敛）

## Multi-Paxos 共识引擎

**协议结构**

- **Phase 1 (Prepare/Promise)**：候选者以唯一 ballot（`round*size+id`）向多数派
  发起 Prepare；Promise 携带接受者完整日志与 commitIndex，候选者据此合并出
  每个未提交槽位的"已选定值"（最高 ballot 优先），空洞槽位补 no-op，然后在
  新 ballot 下重新 Accept 全部待修复槽位并提交。
- **Phase 2 (Accept/Accepted)**：条目被多数派接受即提交；Commit 广播携带
  已提交日志前缀，落后/分区恢复后的跟随者据此对齐前缀并覆写脏槽位。
- **领导者租约**：租约有效期内 Propose 直接走 Phase 2，跳过 Phase 1
  （测试用消息计数器断言 Prepare 数为零增长）；每次多数派达成自动续租。
- **无空洞**：Accept 乱序到达时以 no-op 物理补洞；提交索引只连续前进，
  保证 `[0, commitIndex)` 无空洞。

**安全性论证要点**

- 提交值必在多数派上 ⇒ 任何 Phase 1 多数派必然相交并观测到该值 ⇒
  新 Leader 只能重新提议已选定值（覆写只发生在未提交槽位）。
- 旧 Leader 租约未过期但被分区时，无法集齐多数派 ⇒ 写入失败且不会提交，
  其脏条目在修复阶段被安全覆写。

## 多租户 DRR 调度器

- **加权公平**：每轮为租户充值 `weight*quantum` 赤字，队头任务成本 ≤ 赤字
  即可连续派发；赤字有上限（防止空闲后突发），成本超上限的任务抬升上限
  保证最终可派发。
- **隔离**：每租户独立有界队列（满了返回 `ErrQueueFull` 背压）+ 在途配额
  （超限租户被跳过，不影响其他租户）。
- **并发安全**：全部方法持单锁；`Dispatch`/`Done` 守恒 —— 测试断言
  派发数 == 完成数、在途/排队归零、无 goroutine 泄漏。

## 运行

```sh
go test ./...        # 全部测试
go test -race ./...  # 竞态检测
go run ./cmd/demo    # 运行样例
```

## 运行样例输出

```
=== Multi-Paxos: partition & recovery scenario ===
[1] node0 campaigns and commits v0,v1 under its leader lease
    messages so far: prepares=4 accepts=8 (lease bypasses Phase 1)
  committed:     n0=[v0(b5) v1(b5)] n1=[v0(b5) v1(b5)] n2=[v0(b5) v1(b5)] n3=[v0(b5) v1(b5)] n4=[v0(b5) v1(b5)]
[2] partition {0,1} | {2,3,4}; old leader node0 keeps writing
    old leader propose rejected: paxos: cannot reach a majority (dirty slot stays uncommitted)
    node0 raw log: [v0(b5) v1(b5) dirty(b5)]  <- dirty on minority only
[3] node2 wins majority election, repairs log, commits v2,v3
  committed:     n0=[v0(b5) v1(b5)] n1=[v0(b5) v1(b5)] n2=[v0(b5) v1(b5) v2(b12) v3(b12)] n3=... n4=...
[4] heal; repair campaign backfills minority, overwrites dirty slot
  committed:     n0..n4 全部 = [v0(b5) v1(b5) v2(b12) v3(b12)]
    node0 raw log after repair: [v0(b5) v1(b5) v2(b12) v3(b12)] (dirty value gone)
    node0.IsLeader=false node2.IsLeader=true

=== Multi-tenant DRR scheduler ===
  weights 3:2:1 -> dispatched in window: map[analytics:300 billing:200 batch:100]
  quota isolation: hog capped at 2 in-flight -> stats {Dispatched:12 Completed:10 InFlight:2 Queued:8}
```

## 测试结果（go test -race ./...）

```
ok  multipaxos/paxos       TestBasicConsensus                        基本共识与无空洞提交
ok  multipaxos/paxos       TestLeaseBypassesPhase1                  租约下 Phase 1 零开销（消息计数断言）
ok  multipaxos/paxos       TestProposeRequiresLeadership            非 Leader/租约过期拒绝写入
ok  multipaxos/paxos       TestPartitionNewLeaderRepairsAndOverwrites  ★验收场景：分区换主+修复+覆写脏日志
ok  multipaxos/paxos       TestHoleRepair                           空洞槽位补 no-op 并提交
ok  multipaxos/paxos       TestSplitCampaignMinorityLoses           少数派永远无法当选
ok  multipaxos/paxos       TestConcurrentProposers                  并发 Propose 串行化、日志逐字节一致
ok  multipaxos/scheduler   TestDRRWeightedFairness                  2:1 权重公平（±5%）
ok  multipaxos/scheduler   TestDRRThreeWayWeights                   3:2:1 权重公平
ok  multipaxos/scheduler   TestQuotaIsolation                       在途配额隔离，超限租户被跳过
ok  multipaxos/scheduler   TestQueueFullBackpressure                有界队列背压，邻居不受影响
ok  multipaxos/scheduler   TestDeficitAccumulation                  高成本任务赤字累积后派发/超上限抬升
ok  multipaxos/scheduler   TestCostAccounting                       成本计费：等权重下成本 2x → 吞吐 1/2
ok  multipaxos/scheduler   TestZeroCostTreatedAsOne                 非正成本归一化为 1
ok  multipaxos/scheduler   TestErrorPaths                           未知租户/非法配置/Done 下溢/重复注册/忙时移除
ok  multipaxos/scheduler   TestEmptyScheduler                       空调度器不派发
ok  multipaxos/scheduler   TestConcurrentNoLeak                     ★16 生产者+4 消费者，守恒且零泄漏（含 goroutine 泄漏断言）
ok  multipaxos/scheduler   TestConcurrentFairnessUnderContention    多消费者竞争下权重公平仍成立
```
