# Multi-Paxos 共识日志 + 增量快照引擎（Go，仅标准库）

无单点故障的多数派共识日志协议，带领导者租约快速路径、空洞修补，
以及低停顿的增量快照 / Checkpoint 归档与恢复。

## 结构

| 文件 | 职责 |
| --- | --- |
| `paxos/types.go` | Ballot、LogEntry、Prepare/Promise、Accept/Accepted 等消息 |
| `paxos/node.go` | 共识节点：两阶段提交、租约、空洞修补、快照联动、恢复 |
| `paxos/hub.go` | 同步内存传输层，支持网络分区/愈合模拟 |
| `paxos/kv.go` | 复制状态机（KV），journaling 支撑增量快照 |
| `paxos/snapshot.go` | 增量快照 + Checkpoint 归档（原子写、保留策略、链式恢复） |
| `cmd/demo/main.go` | 5 节点分区换主 + 快照恢复完整演示 |

## 设计要点

- **两阶段共识**：Phase 1（Prepare/Promise）选主并收集多数派日志；
  Phase 2（Accept/Accepted）复制日志条目，多数派接受即提交。
- **领导者租约**：租约有效期内 `Propose` 只跑 Phase 2（测试用
  `Prepares()` 计数断言 Phase 1 被跳过）；租约过期或被更高 ballot
  抢占则自动退位。
- **空洞修补**：新 Leader 的 Phase 1 收集 `(commitIndex, maxSlot]` 内
  所有已接受条目，逐槽位重放最高 ballot 的值；多数派都不知道的槽位
  填 noop，保证日志无空洞地连续提交。
- **脏日志覆写**：少数派上未提交的条目在分区愈合后由新 Leader 以
  更高 ballot 的 Accept 安全覆写；已提交槽位永不被覆盖。
- **增量快照**：状态机 journal 记录自上次快照以来的 op 序列，生成
  增量只是拷贝一个 slice，无需扫描全量状态（低停顿、小开销）；
  增量按 `(from, to]` 链式衔接。
- **Checkpoint 归档**：定期全量 checkpoint（tmp + rename 原子写），
  保留最近 N 个，被覆盖的增量自动剪枝；恢复 = 最新 checkpoint +
  链式回放增量。checkpoint 后日志压缩，落后过多的 follower 通过
  快照传输（InstallSnapshot 式）追赶。
- **无泄漏**：库本身不启动任何 goroutine，所有进度由调用驱动。

## 运行

```sh
go test ./paxos/ -race -count=1 -v   # 全部测试（竞态检测）
go run ./cmd/demo                    # 分区换主 + 快照恢复演示
```

## 测试覆盖

- `TestPartitionReplacesOldLeader`：验收场景——5 节点 2|3 分区，旧
  Leader 被多数派新 Leader 取代，Phase 1 补全未提交槽位，愈合后安全
  覆写少数派脏日志，全集群状态一致。
- `TestLeaseSkipsPhase1` / `TestLeaseExpiryForcesRecampaign`：租约快速
  路径与过期重选。
- `TestHoleRepair`：稀疏日志（slot 1、3 存在，2 缺失）由 Phase 1 填
  noop 并顺序提交。
- `TestConcurrentPropose`：8 goroutine 并发提案，槽位唯一、连续、全部
  提交（`-race`）。
- `TestSyncViaSnapshotTransfer`：follower 落后过 checkpoint 压缩点，
  通过快照传输追赶。
- 快照边界：空归档、无 checkpoint 的增量链、非法区间、保留策略剪枝、
  损坏文件报错、无临时文件残留、并发读写（`-race`）、节点级
  快照-压缩-恢复全生命周期。
- `TestNoGoroutineLeak`：集群反复构建运行后 goroutine 数不增长。
