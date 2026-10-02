# logrepl — 视图变更日志复制器

单进程、离散时间的复制日志（简化 Raft）。不调用网络协议栈：消息投递、
链路故障与心跳全部由外部测试时钟通过 `Cluster.Tick()` 驱动。

## 设计要点

- **选举期数单调自增**：term 只在两处变化——赢得预投票后发起正式选举
  （`startElection` 中 `term++`），或收到更高 term 的消息时被迫跟随
  （`becomeFollower`）。term 永不回退，测试中有逐阶段单调性断言。
- **预投票**：选举计时器超时后只发起预投票（`startPreVote`），本地 term
  不变；只有预投票拿到多数派才真正发起选举。被隔离的节点因此无法抬高
  自己的 term，恢复后也不会扰乱健康集群（接收方带 leader 租约检查：
  半个选举超时内收到过心跳则拒绝预投票）。
- **日志一致性检查**：`AppendEntries` 携带 `(PrevLogIndex, PrevLogTerm)`，
  不匹配则拒绝并携带 `Hint`，领导者逐条回退 `nextIndex` 重试。
- **安全截断**：冲突条目仅当位于 `committed` 之后才会被截断覆写
  （`handleAppend` 中 `idx <= n.committed` 的条目直接跳过）；领导者只对
  当前 term 的条目按多数派推进提交点（`maybeCommit`），保证已提交条目
  在任何视图变更下都不会丢失。

## 运行

```sh
go test ./... -v   # 验收测试 + 单元测试
go run ./cmd/demo  # 端到端运行样例
```

## 验收场景（TestViewChangeScenario / cmd/demo）

1. 3 节点集群，C(N3) 当选 term 1 领导者，提交 3 条日志（e1..e3）；
2. 隔离 C：C 作为过期领导者接受一条永远无法提交的写 `X-uncommitted`
   （index 4 的冲突未提交条目）；多数派 A/B 推进到 term 2 并再提交
   4 条（e4..e7）；隔离期间 C 的 term 保持为 1（预投票生效）；
3. 恢复 C：term 2 领导者的心跳对 C 是高 term 心跳，C 退位为跟随者，
   经一致性检查回退后截断 `X-uncommitted`，覆写为 e4..e7，已提交的
   e1..e3 原样保留。

## 测试结果

```
=== RUN   TestViewChangeScenario
--- PASS: TestViewChangeScenario (0.00s)
=== RUN   TestPreVoteKeepsIsolatedFollowerTermStable
--- PASS: TestPreVoteKeepsIsolatedFollowerTermStable (0.00s)
=== RUN   TestCommittedEntriesAreNeverTruncated
--- PASS: TestCommittedEntriesAreNeverTruncated (0.00s)
=== RUN   TestRejectsStaleTermMessages
--- PASS: TestRejectsStaleTermMessages (0.00s)
=== RUN   TestLogConsistencyCheck
--- PASS: TestLogConsistencyCheck (0.00s)
PASS
ok  	logrepl	0.636s
```

## 运行样例（go run ./cmd/demo）

```
== phase 1: term 1, 3 entries committed on all nodes
  N1 term=1 role=Follower committed=3 log=[e1(t1) e2(t1) e3(t1)]
  N2 term=1 role=Follower committed=3 log=[e1(t1) e2(t1) e3(t1)]
  N3 term=1 role=Leader   committed=3 log=[e1(t1) e2(t1) e3(t1)]

== phase 2: C isolated with an uncommitted write
  N3 term=1 role=Leader   committed=3 log=[e1(t1) e2(t1) e3(t1) X-uncommitted(t1)]
  ...

== phase 3: A/B advanced to term 2 (leader=N1), 4 entries committed
  N1 term=2 role=Leader   committed=7 log=[e1(t1) e2(t1) e3(t1) e4(t2) e5(t2) e6(t2) e7(t2)]
  N3 term=1 role=Leader   committed=3 log=[e1(t1) e2(t1) e3(t1) X-uncommitted(t1)]
  ...

== phase 4: C healed, higher-term heartbeat forced rollback of X
  N3 term=2 role=Follower committed=7 log=[e1(t1) e2(t1) e3(t1) e4(t2) e5(t2) e6(t2) e7(t2)]
```
