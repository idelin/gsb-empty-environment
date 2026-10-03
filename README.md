# Multi-Paxos 多数派共识日志 + 自适应动态阈值伸缩（Go 标准库）

无单点故障的分布式共识日志引擎，仅使用 Go 标准库。

## 架构

```
paxos/     Multi-Paxos 共识核心（零后台 goroutine，同步内存传输）
adaptive/  负载感知的自适应阈值控制器（AIMD + 冷却滞回 + 熔断器）
cmd/demo/  可运行样例：5 节点分区场景 + 自适应调参演示
```

### 共识引擎（paxos）

- **两阶段协议**：Phase 1 `Prepare/Promise`（`Node.Campaign`），Phase 2 `Accept/Accepted`（`Node.accept`）。
- **领导者租约优化**：租约有效期内 `Propose` 跳过 Phase 1，单轮直接提交连续 `LogEntry`；租约过期或出现更高 ballot 时自动重新竞选（`TestLeaseSkipsPhaseOne` / `TestLeaseExpiryForcesNewCampaign`）。
- **空洞修补**：新 Leader 的 Promise 携带各副本完整日志，逐槽位选取最高 ballot 的值重放 Phase 2；无值空洞填 `NoOpValue`，保证提交前缀连续无空洞（`TestHoleRepairAdoptsHighestBallot`）。
- **分区安全**：少数派旧 Leader 无法凑齐多数派，脏日志永不提交；愈合后 `SyncFollowers` 用更高 ballot 的 Accept 安全覆写少数派脏槽位（`TestPartitionNewLeaderRepairsAndOverwritesDirtyLog`）。
- **防泄漏**：核心不启动任何 goroutine，同步 RPC 式传输，`TestNoGoroutineLeak` 严格断言 goroutine 数不增长。

### 自适应控制器（adaptive）

根据外部压力指标（`Metrics{Load, QueueDepth, RTT, ErrorRate}`）自调节内部参数
（`Limits{MaxInFlight, BatchSize, LeaseDuration}`）：

- **AIMD 伸缩**：低负载加性增、高负载/队列溢出乘性减，双向钳位在 `[Min, Max]`。
- **冷却滞回**：两次变更间的最小间隔，规避抖动振荡。
- **熔断防雪崩**：错误率 ≥ 阈值即跳闸，参数钉在地板值，窗口期后半开恢复。
- **租约感知**：Leader 租约 = `BaseLease + 3×RTT`，高负载加长（减少换主抖动）、低负载缩短（加快故障转移），钳位在 `[MinLease, MaxLease]`。

## 运行

```bash
go test ./... -race -count=1   # 全部测试（竞态检测）
go run ./cmd/demo              # 运行样例
```

## 测试结果

```
ok  multipaxos/adaptive   1.771s   (11 个测试：边界水位线、零/极端指标、冷却滞回、
                                      熔断生命周期、地板钳位、RTT 租约、并发安全、无泄漏)
ok  multipaxos/paxos      1.818s   (7 个测试：多数派提交、租约跳过 Phase 1、租约过期重选、
                                      分区换主覆写脏日志、空洞修补、并发提案、无泄漏)
```

## 样例输出（go run ./cmd/demo）

```
=== Part 1: Multi-Paxos consensus (5 nodes) ===
leader=n0 committed=["set x=1" "set y=2"] (prepares handled cluster-wide: 5)

--- partition: {n0,n1} | {n2,n3,n4} ---
old leader (minority) cannot commit: dirty entry stays uncommitted
new leader elected by majority: n2
majority side committed=["set x=1" "set y=2" "set z=3"]

--- heal: old leader's dirty log is safely overwritten ---
n0 committed=["set x=1" "set y=2" "set z=3"] leader=false
n1 committed=["set x=1" "set y=2" "set z=3"] leader=false
n2 committed=["set x=1" "set y=2" "set z=3"] leader=true
n3 committed=["set x=1" "set y=2" "set z=3"] leader=false
n4 committed=["set x=1" "set y=2" "set z=3"] leader=false

=== Part 2: adaptive dynamic threshold scaling ===
warm-up, low load          -> MaxInFlight=12   Batch=9   Lease=156ms  breaker=false
low load                   -> MaxInFlight=20   Batch=17  Lease=156ms  breaker=false
rising load                -> MaxInFlight=20   Batch=17  Lease=174ms  breaker=false
high load                  -> MaxInFlight=14   Batch=11  Lease=405ms  breaker=false
errors spike (breaker)     -> MaxInFlight=7    Batch=5   Lease=50ms   breaker=true
breaker window             -> MaxInFlight=7    Batch=5   Lease=50ms   breaker=true
```
