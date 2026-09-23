# jointpaxos：联合多数阶段切换的成员变更共识（单进程事件模拟）

在固定旧成员集合 `Cold` 与新成员集合 `Cnew`（可互不相交）之间做共识重配置。
切换必须经过**联合多数阶段**，不允许从旧多数直接切到互不相交的新多数：

```
view 0: plain(Cold)  --决定-->  view 1: joint(Cold, Cnew)  --决定-->  view 2: plain(Cnew)
```

每个视图是一个独立的单决议 Paxos 实例；视图 v 决定的值里携带下一视图的配置，
配置链本身由共识决定。每条消息都带视图版本号（`Msg.View`），节点只处理当前
视图的消息。通信、故障（丢包/延迟/崩溃/重启）与时间全部由单进程离散事件
模拟器（`Sim`/`Network`）控制，仅使用 Go 标准库。

## 文件

- `sim.go` — 虚拟时钟、事件队列、网络（丢包/延迟故障注入钩子）
- `config.go` — 配置、联合多数仲裁规则、`validTransition` 切换守卫
- `node.go` — 节点：Paxos 提议者/接受者、视图过滤、崩溃恢复、追赶
- `jointpaxos_test.go` — 安全性与活性证据（`go test`）
- `cmd/demo/main.go` — 可运行的脚本化场景（含切换中断与恢复）

## 关键机制

- **联合多数**：`Config.Quorum` 在联合视图下要求旧集合多数 **且** 新集合多数，
  仅单侧多数永远不能提交（`config.go`）。
- **视图版本**：`Node.Handle` 丢弃 `View < CurrentView` 的迟到消息（计数
  `StaleIgnored`），旧视图的迟到投票不会计入新视图。
- **拒绝直接切换**：`validTransition` 只允许 `plain→joint→new`（或相交集合间
  的 plain→plain）。接受者在 `Accept` 与 `Commit` 两处都校验，互不相交的
  直接跳转被拒绝（计数 `RejectedReconfig`）。
- **中断可恢复**：承诺/接受/提交状态在 `Store`（模拟稳定存储）中，崩溃只丢
  易失提议者状态；重启后重载并继续，落后节点通过"过期消息触发决策回传 +
  重启 ping"追赶。

## 安全性证据

**仲裁相交（论证）**：联合视图的任意两个仲裁都相交（枚举验证见
`TestQuorumIntersection`）；而互不相交两集合各自的多数可以不相交——这正是
直接切换不安全、必须走联合阶段的原因。视图内是标准 Paxos：更高选票的第一
阶段必然碰到前一仲裁中已接受值的节点，从而只能继承已选值。跨视图：视图
v+1 的配置只能由视图 v 的已提交值安装，联合视图的提交包含 `Cnew` 的多数，
与视图 2 的任意仲裁相交，因此配置链与决定值唯一。

**测试证据**（`jointpaxos_test.go`）：

- `TestQuorumIntersection` — 枚举全部联合仲裁，两两相交；不相交多数可互不相交。
- `TestTransitionGuard` — 守卫真值表：直接不相交切换拒绝，分阶段路径放行。
- `TestSeparateMajoritiesCannotCommit` — 旧集合多数接受 X、新集合多数接受 Y，
  联合条件不满足，任何节点都未提交。
- `TestJointSwitchBlockedByPartition` — 旧/新网络分区时联合视图无法提交。
- `TestLateVotesFromOldViewIgnored` — 延迟 40 拍的旧视图投票与注入的过期
  Accept 均被忽略，已提交值不变。
- `TestDirectDisjointSwitchRejected` — 恶意提议直接跳到不相交新集合：无提交、
  无视图前进、伪造 Commit 同样被拒。
- `TestSafetyUnderRandomFaults` — 50 个随机种子（15% 丢包、随机延迟、随机
  崩溃/重启），每视图已提交值全局一致、配置链合法、零冲突记录。

## 活性证据

- `TestFaultFreeLiveness` — 无故障时完整 `old→joint→new` 切换在 **t=19**
  （有界虚拟时间）前在所有节点提交，命令序列一致。
- `TestJointSwitchBlockedByPartition`（后半段）— 分区恢复后切换继续并完成。
- `TestRecoveryFromInterruptedSwitch` — 联合阶段中途崩溃到联合仲裁不复存在，
  停滞；节点从持久状态重启后切换恢复，三视图全部提交。
- `TestSafetyUnderRandomFaults` — 随机故障下 39/50 次运行在时限内完成全部
  切换（其余运行安全地停滞，无任何冲突提交）。

## 运行

```sh
go test -v ./...     # 全部证据测试
go run ./cmd/demo    # 脚本化演示：联合切换 + 中途崩溃 + 恢复
```

## 运行样例（`go run ./cmd/demo`，节选）

```
[t=   4] a COMMITS view 0: {cmd="op-1" next=joint(old=[a b c],new=[x y z])}
[t=   4] a enters view 1  config=joint(old=[a b c],new=[x y z])
[t=   4] a ignores stale Accepted from c (view 0 < current 1)
[t=   6] a CRASHES
[t=   6] x CRASHES
[t=  15] b starts Prepare for view 1 ballot 1
[t=  19] b COMMITS view 1: {cmd="op-2" next=set[x y z]}
[t=  34] y COMMITS view 2: {cmd="op-3"}
[t=  40] a restarts, resuming view 1
[t=  42] a learns commit of view 1: {cmd="op-2" next=set[x y z]}
[t=  42] a enters view 2  config=set[x y z]
=== final state ===
a: view=2 commits=[op-1 op-2 op-3] stale-ignored=11 rejected-reconfig=0 violations=[]
b: view=2 commits=[op-1 op-2 op-3] stale-ignored=6  rejected-reconfig=0 violations=[]
c: view=2 commits=[op-1 op-2 op-3] stale-ignored=6  rejected-reconfig=0 violations=[]
x: view=2 commits=[op-1 op-2 op-3] stale-ignored=5  rejected-reconfig=0 violations=[]
y: view=2 commits=[op-1 op-2 op-3] stale-ignored=5  rejected-reconfig=0 violations=[]
z: view=2 commits=[op-1 op-2 op-3] stale-ignored=5  rejected-reconfig=0 violations=[]
```

## 测试结果（`go test -v ./...`）

```
--- PASS: TestQuorumIntersection (0.00s)
--- PASS: TestTransitionGuard (0.00s)
--- PASS: TestSeparateMajoritiesCannotCommit (0.00s)
--- PASS: TestJointSwitchBlockedByPartition (0.00s)
--- PASS: TestLateVotesFromOldViewIgnored (0.00s)
--- PASS: TestDirectDisjointSwitchRejected (0.00s)
--- PASS: TestSafetyUnderRandomFaults (0.16s)
    jointpaxos_test.go: 39/50 random-fault runs completed the full switch
--- PASS: TestFaultFreeLiveness (0.00s)
    jointpaxos_test.go: full old->joint->new switch committed everywhere by t=19
--- PASS: TestRecoveryFromInterruptedSwitch (0.00s)
PASS
ok      jointpaxos
```
