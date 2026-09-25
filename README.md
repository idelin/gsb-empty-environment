# raftdemo

Raft 日志复制核心 + 单进程事件模拟器，仅使用 Go 标准库。

## 结构

- `raft/raft.go` — 复制协议核心：`AppendEntriesRequest` 携带 `PrevLogIndex`/`PrevLogTerm`
  做前缀一致性检查；冲突时删除未提交后缀再追加；已提交条目（`idx <= commitIndex`）拒绝覆盖；
  拒绝时返回追赶提示（`ConflictTerm`/`ConflictIndex`），领导者据此跳跃式回退 `nextIndex`；
  重复投递幂等（同 `(index, term)` 条目直接跳过）。
- `raft/sim.go` — 单进程事件模拟器：逻辑时钟 + 事件堆驱动时间；`send` 统一控制消息延迟、
  丢失、重复与分区；`Crash`/`Restart` 模拟故障（持久态落 `Disk`，重启后恢复）。
- `raft/raft_test.go` — 全部测试（`go test`）。
- `cmd/demo` — 可运行的场景样例。

## 安全性与活性证据

- 安全性：`TestSafetyUnderRandomFaults` 在随机崩溃/重启/分区/丢包/重复投递下持续校验
  状态机安全不变量（已提交条目不可变、节点间已提交前缀不发散），并以零违规收尾；
  `TestCommittedEntriesCannotBeOverwritten`、`TestPrefixMismatchRejected`、
  `TestElectionRestrictionStaleCandidate` 给出机制级保证。
- 活性：`TestLivenessAfterStability` 证明无故障时有界时间内选出领导者并提交命令；
  `TestSafetyUnderRandomFaults` 的后半段证明故障停止后集群收敛到同一已提交前缀。

## 运行

```sh
go test ./raft/ -v
go run ./cmd/demo
```
