# 日志可回收前缀 / 快照重入（单进程事件模拟器）

纯 Go、仅标准库实现。节点间通信、故障（宕机/重启/分区/丢包）、时间推进
全部由一个确定性单进程事件模拟器控制（`sim/simulator.go`）；协议逻辑在
`sim/protocol.go`，运行样例在 `cmd/demo`。

## 规则与实现位置

| 要求 | 实现 |
| --- | --- |
| 按副本“已持久确认位置”计算可回收前缀 | `System.tryAdvance`：`reclaim = min(各活跃成员 durableLast)`，含 leader 自身 |
| 确认单调 | follower 只在 flush（持久化）后回 Ack；leader 用 `max` 更新 `peer.match`；`MatchSeries`/`ReclaimSeries` 记录历史并被测试断言单调 |
| 内存中接收但未持久不能确认 | `stage` 仅写入 `node.pending`；`flush` 前不改变 durable，不回 Ack；`Crash` 丢弃 pending；checker 断言 `match <= durableLast` |
| 慢副本未移出时阻止回收 | 活跃 follower（含分区/慢链路）的 durableLast 参与 min；状态未知时保守取 0 |
| 离线副本可“明确移出”，但要记录快照重入要求 | `System.Evict`：置 `evicted`、写 `EvictRecord{ReclaimPoint, RequiredSnap}`，此后它离开回收集合 |
| 副本重入不得要求已删除日志 | 当 `peer.match < leader.snap.LastIndex` 时 leader 只能发 `InstallSnapshot`（`broadcast`）；`Readd` 清空副本，从快照重入；乱序旧 Append 落在快照前缀内被直接丢弃，永不回读已删除日志 |
| 通信/故障/时间在单进程事件模拟器中控制 | `Simulator`：逻辑时钟 + 事件堆、链路延迟/抖动/丢包/分区、`Crash/Restart`、可穿越宕机窗口的重启定时器 |

## 可回收前缀的形式化陈述

设活跃成员集合 `A`（未被移出、且不处于 `joining` 快照重入态），
`d_i` 为成员 `i` 的**已持久**日志末位下标，则

```
reclaim(t) = min_{i in A} d_i(t)
```

- 只有 `reclaim` 增长时才生成新快照并截断物理日志，故 `reclaim(t)` 单调不减。
- 移出成员 `f` 是把它从 `A` 中删除的**唯一**途径（显式 `Evict`）。
- 重入成员在快照安装完成、追上当前末位之前保留在 `joining`，不计入 `A`；
  因此重入过程中回收点只依据现有正式成员推进，不会等待一个空节点，
  也不会要求它提供已被删除的日志。

## 安全性证据（不变量，由 SafetyChecker 在每个观测时刻检查）

1. **回收点不回退**：`leader.snap.LastIndex` 单调不减（`TestMonotonicConfirmations`，
   且 checker 全时刻检查）。
2. **不越过任何活跃副本的持久点**：leader 快照下标 `<=` 所有活跃成员 durableLast
   （慢副本阻塞：`TestSlowFollowerBlocksRecycle`、checker 第 3 条）。
3. **未持久不确认**：leader 记录的 `peer.match <= follower.durableLast`，且 match 序列单调
   （`TestMemoryIsNotAcked`：在 follower flush 前宕机，内存条目丢失，重启后重新同步）。
4. **状态机一致**：每个节点快照哈希 = 对命令序列前缀的 FNV-1a 摘要；最终所有节点哈希相同。
5. **不引用已删除日志**：跨压缩边界只能走快照（`broadcast` 的 InstallSnapshot 分支）；
   乱序旧消息落在快照前缀内丢弃（`TestNeverAsksForDeletedLog` 全程探针计数必须为 0；
   `TestRejoinUsesSnapshotNoDeletedLog` 验证清空副本仅靠快照+新尾部追平）。

## 活性证据（条件性：链路有界延迟、丢包最终可通、无永久宕机）

- leader 每 `heartbeatPeriod` 全量重发：未知 follower 发 Probe，落后者发快照或日志，
  未确认条目在每个心跳重传；丢包只会延迟不会永久阻止（`TestLivenessUnderLoss`：30%
  丢包 + 抖动下全部条目最终在所有节点持久，回收点最终到达末位）。
- follower 重启后主动上报持久位置，leader 据此重建易失的 peer 进度。
- `TestChaosSafety`（8 个固定随机种子，4 副本：宕机/重启/分区/移出/清空重入）在恢复
  稳定窗口后断言：所有节点 durableLast 到达末位、哈希一致、回收点到达末位。

说明：这是带固定稳定 leader 的最小模型，故意不包含选举；活性前提是 leader 本身最终在线。
leader 也会宕机/重启，但其持久日志与成员记录保留，重启后由心跳与状态上报收敛。

## 运行

```sh
go test ./...            # 全部测试（含 8 个混沌种子）
go test -v ./...         # 查看每个用例
go run ./cmd/demo        # 脚本化运行样例
```

## 场景脚本（demo）

1. 三节点健康，提交 6 条 → 回收点到 6；
2. 分区 node2，再提交 4 条 → 回收点停在 6（慢/离线活跃副本阻塞）；
3. 显式移出 node2（记录“重入需要 snapshot>=10”）→ 回收点到 10；
4. 再提交 3 条（尾部到 13），清空 node2 并重新加入 → 安装快照到 13、追平、
   提升为正式成员；三节点状态机哈希一致。
