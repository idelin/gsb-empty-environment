# Multi-Paxos 共识日志引擎 + 零分配缓存/对象池

纯 Go 标准库实现，无第三方依赖。两个独立组件：

- `paxos/` — Multi-Paxos 多数派共识与无空洞日志提交引擎
- `cache/` — 低延迟零内存分配缓存与泛型对象池

## Multi-Paxos 共识引擎（paxos/）

- **两阶段协议**：Phase 1（Prepare/Promise）+ Phase 2（Accept/Accepted）。
  Promise 携带各槽位已接受的值，保证新 Leader 重提多数派已接受的值（安全性）。
- **领导者租约快速路径**：租约有效期内跳过 Phase 1，连续 LogEntry 仅用
  Phase 2 单轮提交；每次多数派成功自动续租。`TestLeaseSkipsPhase1` 断言
  租约期内 Prepare 消息数为零增长。
- **无空洞提交**：`commitIndex` 只在连续已提交前缀上推进；新 Leader 的
  Phase 1 扫描 `[commitIndex+1, maxSlotSeen]`，用 no-op 填补空洞、重提
  已接受值，修复日志（`TestGapRepairFillsHoles`）。
- **脏日志安全覆写**：少数派上未提交的脏条目在分区恢复后通过 commit
  广播 + 拉取式 catch-up sync 被多数派已提交值覆写；已提交值永不改变
  （冲突即 panic 的 SAFETY 断言）。
- **无单点故障**：任意多数派即可选主并继续提交；少数派无法选主
  （`TestNoMajorityCampaignFails`），旧 Leader 见到更高 ballot 即退位。

### 验收场景（paxos/paxos_test.go）

`TestPartitionNewLeaderRepairsAndOverwritesDirtyLog`：

1. 5 节点集群，节点 1 为 Leader，提交 a1..a3；
2. 分区 `{1,2}|{3,4,5}`，旧 Leader 的 `X@4` 仅被少数派接受（脏日志）；
3. 多数派选举节点 3 为新 Leader，Phase 1 修复日志，复用槽位 4 提交 `b4`；
4. 分区恢复：旧 Leader 提案被拒（`ErrStaleBallot`）并退位，catch-up sync
   覆写少数派脏日志，5 节点收敛到相同的无空洞已提交前缀。

## 零分配缓存与对象池（cache/）

- **Cache**：固定容量分片缓存，CLOCK（二次机会）淘汰。全部内存在构造时
  预分配（每槽位固定 `MaxValueSize` 缓冲），热路径 `Get`/`Put`（命中与
  覆写）**零堆分配**（`TestZeroAllocHotPath` 断言 `AllocsPerRun == 0`）。
  内存严格有界，绝无泄漏（`Len() <= Capacity` 恒成立）。
- **Pool[T]**：基于 `sync.Pool` 的泛型对象池，`Put` 时重置对象防止状态
  串扰；`InUse` 计数器支撑严格无泄漏断言（平衡使用后必须为 0）。

## 运行

```sh
go test ./...          # 全部测试
go test -race ./...    # 竞态检测（并发安全验证）
go run ./cmd/demo      # 端到端演示：分区 -> 换主 -> 修复 -> 覆写 -> 收敛
```

## 测试结果（go test -race -count=1）

```
ok  multipaxos/cache  (18 个测试：边界、淘汰、统计、零分配、并发、无泄漏)
ok  multipaxos/paxos  (8 个测试：选主、租约快路径、分区验收、空洞修复、安全性)
```

Demo 实测：缓存 100k 次 Get+Put 热路径 `allocs/op = 0.0000`；
对象池 100k 次 Get+Put `allocs/op = 0.0000`，`in-use = 0`（无泄漏）。
