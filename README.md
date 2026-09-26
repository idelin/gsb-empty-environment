# raftsim — Raft 日志复制的单进程事件模拟

仅用 Go 标准库实现的 Raft AppendEntries 日志复制，通信、故障与时间全部
由单进程事件模拟器控制，运行结果对给定随机种子完全确定。

## 结构

- `raft.go` — Raft 节点：选举、AppendEntries、提交与状态机应用
- `sim.go` — 事件模拟器：逻辑时钟、消息延迟/丢失、崩溃/重启、网络分区
- `raft_test.go` — 安全性与活性证据（`go test`）
- `cmd/demo/main.go` — 可运行的分区/恢复演示（`go run ./cmd/demo`）

## 关键机制

- **前项索引和任期**：每条 AppendEntries 携带 `PrevIndex`/`PrevTerm`，
  跟随者先做一致性检查，前缀不匹配拒绝接尾。
- **冲突截断**：发现冲突条目时仅删除**未提交**后缀再追加；相同条目
  跳过，重复追加幂等。
- **禁止覆盖已提交条目**：冲突下标 `<= commitIndex` 直接 panic
  （`SAFETY`），测试中作为安全断言验证。
- **追赶提示**：拒绝时返回 `(ConflictTerm, ConflictIndex)`，领导者据此
  一次性回退 `nextIndex`，而非逐条探测。
- **提交规则**：领导者只对当前任期的条目按复制计数提交（Raft 图 8
  规则）；当选即追加 no-op 锚定任期。

## 安全性证据（测试即证明义务）

- `TestDuplicateAppendIsIdempotent` — 重复追加/重复提交通知不改变日志
- `TestPrefixMismatchRejectedWithHint` — 前缀不匹配拒绝且日志不变，
  提示值正确（冲突任期/首个冲突下标、日志过短）
- `TestConflictTruncatesUncommittedSuffix` — 仅截断未提交后缀
- `TestCommittedEntryOverwritePanics` — 覆盖已提交条目被拦截
- `TestRandomizedSafetyAndLiveness` — 丢包/崩溃/重启/分区混沌场景下，
  `Sim.Check` 在**每个事件后**校验所有节点已提交前缀两两一致

## 活性证据

- `TestElectionAndCommitLiveness` — 选主并提交命令到全部节点
- `TestShortAndLongLogsConvergeAfterHeal` — 少数派短日志与多数派长日志
  在分区恢复后收敛到同一已提交前缀，未提交分歧条目被丢弃
- `TestRandomizedSafetyAndLiveness` — 故障全部修复后集群恢复进展，
  最终命令在全体节点提交且一致

## 运行

```sh
go test -v ./...   # 测试（安全性 + 活性证据）
go run ./cmd/demo  # 运行样例：选举 → 复制 → 分区 → 恢复 → 追赶
```
