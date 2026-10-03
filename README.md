# Multi-Paxos 共识日志引擎 + 分层分片局部性感知路由引擎 (TypeScript)

无单点故障的分布式共识日志协议与超大规模拓扑下的虚拟分片路由引擎。
纯 TypeScript 实现，零第三方依赖，使用 Node.js 内置测试运行器 (`node:test`) 验证。

## 运行要求

- Node.js >= 22.6（使用内置 TypeScript 类型擦除运行 `.ts`，无需编译步骤与任何 npm 依赖）

## 快速开始

```bash
# 运行全部测试（Node.js 内置测试运行器）
npm test
# 等价于: node --experimental-strip-types --test test/*.test.ts

# 运行端到端样例（网络分区 + 局部性路由）
npm run demo
# 等价于: node --experimental-strip-types examples/demo.ts
```

## 架构

### 1. Multi-Paxos 多数派共识与无空洞日志提交引擎 (`src/paxos/`)

| 模块 | 职责 |
| --- | --- |
| `node.ts` | Multi-Paxos 副本：Prepare/Promise（Phase 1）、Accept/Accepted（Phase 2）、领导者租约、空洞修补、无空洞提交 |
| `network.ts` | 确定性内存消息总线：队列化投递（`flush()` 泵送）、网络分区/恢复模拟、消息统计 |
| `cluster.ts` | 测试/仿真线束：共享 `ManualClock` 与 `Network` 的节点集群 |
| `clock.ts` | 可注入时钟（`ManualClock` 用于确定性测试，`SystemClock` 用于真实运行） |

**核心机制**

- **两阶段共识**：经典的 Prepare/Promise 与 Accept/Accepted；选票 `(term, nodeId)` 全序。
- **领导者租约快速路径**：租约有效的 Leader 对连续 LogEntry 直接发起 Phase 2，
  完全绕过 Phase 1（测试断言 `Prepare` 消息数为 0）；每次多数派确认自动续租。
  租约过期后 Leader 先重跑 Phase 1 重新验证授权再提交。
- **空洞修补**：新 Leader 在 Phase 1 收集多数派的完整日志快照，
  对 `[minQuorumCommitIndex+1, maxSeenSlot]` 内每个未决槽位采用最高选票的值重新提案，
  缺口填 no-op；少数派上的脏（未提交）日志在收到更高选票的 Accept/Commit 时被安全覆写。
- **无空洞提交**：`commitIndex` 只沿连续已提交前缀推进，绝不允许跳槽。

### 2. 分层分片与局部性感知路由引擎 (`src/sharding/`)

| 模块 | 职责 |
| --- | --- |
| `topology.ts` | 分层拓扑：region → zone → node；链路代价模型（同节点 0 / 同 zone 1 / 同 region 5 / 跨 region 10，可覆写） |
| `shardmap.ts` | 不可变虚拟分片快照：key → vshard（FNV-1a + fmix32）→ 副本组（rendezvous 哈希，最小迁移） |
| `router.ts` | 局部性感知路由：读请求路由到层级代价最低的副本，写请求固定主副本；批量规划按目标节点聚合以降低跨节点通信 |
| `engine.ts` | 分片引擎：异步互斥锁串行化拓扑变更，写时复制发布新快照，产生迁移计划 |
| `mutex.ts` | Promise 链异步互斥锁（FIFO、无重叠、异常不死锁） |

**并发安全**：所有拓扑变更经 `AsyncMutex` 串行化并原子地（同步临界区）完成
"拓扑变更 + 快照重建"，路由读取每次调用捕获不可变快照，读写无锁、无撕裂。

## 验收场景（`test/partition.test.ts`）

5 节点集群 `{N1..N5}`：

1. N1 当选 Leader（term 1），租约内快速路径提交槽位 1、2；
2. 网络分区 `{N1,N2} | {N3,N4,N5}`，旧 Leader 在少数派继续接受写（槽位 3 脏数据），无法提交；
3. 租约过期，多数派选出新 Leader N3（term 2），Phase 1 补全未决槽位；
4. 新 Leader 提交槽位 3、4；分区恢复后旧 Leader 降级为 follower；
5. 全部 5 个副本收敛到完全一致的日志，少数派脏日志被安全覆写。

## 测试覆盖（32 个用例，全部通过）

- **共识**：选举与提交、租约快速路径跳过 Phase 1、租约过期重选、空洞 no-op 填充与
  可见值收养、并发竞选唯一 Leader、无空洞提交不变式、非 Leader 拒绝写、分区验收场景 x3。
- **分片**：虚拟分片均衡分布、副本数与去重、RF 钳制、非法配置/查找边界、空集群、
  空 id/重复节点/未知节点、空键/Unicode 键/超长键哈希确定性。
- **路由**：层级代价最优性（逐键断言）、同 zone 优先、跨 region 延迟覆写、写主读近、
  批量聚合无丢失、本地/远端统计。
- **并发与泄漏**：互斥锁 FIFO 与排他性、异常不死锁、30 并发变更 x 600 并发路由无丢失更新、
  版本链严格递增、失败变更不污染状态、dispose 后零监听器/零队列/零挂起（严格无泄漏断言）。

## 样例输出

```
=== Part 1: Multi-Paxos consensus log (5 nodes) =================
[elect] N1 becomes leader, term=1, lease valid=true
[commit] fast path (lease, phase 2 only): commitIndex=2
[partition] {N1,N2} isolated from {N3,N4,N5}
[old leader] accepts write in minority, commitIndex stuck at 2 (cannot commit)
[lease] old leader lease expired: true
[elect] N3 becomes leader, term=2 (phase 1 repaired undecided slots)
[new leader] majority commits slots 3-4, commitIndex=4
[heal] old leader role=follower, all replicas converged:
  N1: commitIndex=5 log=[set user:1 = alice | ... | set user:5 = erin]
  ...（N2-N5 完全一致）

=== Part 2: Hierarchical sharding + locality-aware routing ======
[topology] 12 nodes across 3 regions x 2 zones
[shards] 4096 virtual shards, replicationFactor=3
[locality] 30000 reads from random clients:
  same-zone:    46.0%
  same-region:  28.4%
  cross-region: 25.6%
[rebalance] +1 node -> moved 945/4096 shards (23.1%, ideal ~23.1% for RF=3)
```

```
# tests 32
# pass 32
# fail 0
```
