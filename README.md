# 视图变更日志复制器（View-Change Log Replicator）

单进程、离散时间的复制日志实现（Raft 风格），仅使用 Go 标准库。
不调用任何网络协议栈：消息投递、链路故障与心跳全部由测试时钟（`Sim.Tick`）驱动。

## 设计要点

- **选举期数单调自增**：`Node.Term` 只增不减；收到更高 term 的消息即转为 follower。
- **预投票（Pre-Vote）**：选举前先发起预投票，预投票不改变本节点 term。
  被隔离的节点拿不到多数派授权，term 不会膨胀，恢复后也不会干扰现任主。
- **日志一致性检查**：`Append` 携带 `(PrevLogIndex, PrevLogTerm)`，跟随者校验失败即拒绝，
  领导者回退 `next` 游标重试，直到找到共同前缀。
- **安全截断**：冲突条目仅从分歧点截断且仅限未提交部分；试图截断已提交条目会直接
  `panic`（安全哨兵），保证已提交数据永不丢失。
- **提交规则**：仅当某条目被多数派存储且属于当前 term 时才推进 `CommitIndex`
  （旧 term 条目随之间接提交）。
- **分区多数派可推进**：多数派分区继续选举、提交；旧主恢复后收到高 term 心跳，
  强制回滚其冲突的未提交日志并采用新主日志。

## 文件

- `raft.go` — 节点状态机：选举/预投票、日志复制、一致性检查、安全截断。
- `sim.go` — 离散时间网络模拟器：`Tick` 驱动心跳与超时，`Isolate/Heal` 模拟分区，`Send` 注入消息。
- `raft_test.go` — 验收场景与单元测试。
- `cmd/demo/main.go` — 运行样例，打印三阶段全过程。

## 运行

```sh
go test -v ./...    # 运行测试
go run ./cmd/demo   # 运行样例
```

## 运行样例输出（`go run ./cmd/demo`）

```
== phase 1: 3-node cluster elects a leader in term 1 ==
tick=10: node 3 is Leader of term 1
committed 3 entries on every node:
  node 1: Follower term=1 commit=3 log=[e1(t1) e2(t1) e3(t1)]
  node 2: Follower term=1 commit=3 log=[e1(t1) e2(t1) e3(t1)]
  node 3: Leader   term=1 commit=3 log=[e1(t1) e2(t1) e3(t1)]

== phase 2: partition node 3; majority {1,2} moves to term 2 ==
tick=35: node 1 is Leader of term 2; node 3 still term 1 (pre-vote blocked term inflation)
majority committed 4 more entries; node 3 holds 2 uncommitted conflicting entries:
  node 1: Leader   term=2 commit=7 log=[e1(t1) e2(t1) e3(t1) a4(t2) a5(t2) a6(t2) a7(t2)]
  node 2: Follower term=2 commit=7 log=[e1(t1) e2(t1) e3(t1) a4(t2) a5(t2) a6(t2) a7(t2)]
  node 3: Leader   term=1 commit=3 log=[e1(t1) e2(t1) e3(t1) c4(t1) c5(t1)]

== phase 3: heal node 3 and inject a higher-term heartbeat ==
injecting heartbeat: from=1 to=3 term=2 prevLogIndex=3 entries=4 leaderCommit=7
node 3 rolled back its uncommitted c4/c5 and adopted the leader's log:
  node 1: Leader   term=2 commit=7 log=[e1(t1) e2(t1) e3(t1) a4(t2) a5(t2) a6(t2) a7(t2)]
  node 2: Follower term=2 commit=7 log=[e1(t1) e2(t1) e3(t1) a4(t2) a5(t2) a6(t2) a7(t2)]
  node 3: Follower term=2 commit=7 log=[e1(t1) e2(t1) e3(t1) a4(t2) a5(t2) a6(t2) a7(t2)]

committed entries preserved on node 3: [e1(t1) e2(t1) e3(t1)]
```

## 测试结果（`go test -v ./...`）

```
=== RUN   TestViewChangeScenario
--- PASS: TestViewChangeScenario (0.00s)
=== RUN   TestPreVoteKeepsTermStable
--- PASS: TestPreVoteKeepsTermStable (0.00s)
=== RUN   TestAppendConsistencyCheckAndRetry
--- PASS: TestAppendConsistencyCheckAndRetry (0.00s)
=== RUN   TestTruncateCommittedEntryPanics
--- PASS: TestTruncateCommittedEntryPanics (0.00s)
=== RUN   TestOneVotePerTerm
--- PASS: TestOneVotePerTerm (0.00s)
PASS
ok  	raft	0.107s
```

- `TestViewChangeScenario` — 验收场景：term 1 提交 3 条 → 隔离 C 后多数派推进到 term 2
  提交 4 条 → 恢复 C 注入高 term 心跳，C 覆写未提交冲突日志且已提交条目不丢失；
  每个 tick 断言 term 单调与已提交前缀一致。
- `TestPreVoteKeepsTermStable` — 被隔离节点 term 不膨胀，多数派继续提交，恢复后追平。
- `TestAppendConsistencyCheckAndRetry` — 一致性检查拒绝 + 领导者回退重试直至日志收敛。
- `TestTruncateCommittedEntryPanics` — 安全哨兵：截断已提交条目必须 panic。
- `TestOneVotePerTerm` — 每个 term 至多投出一票。
