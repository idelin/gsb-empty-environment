# Multi-Paxos 共识日志引擎 + 形式化约束巡检与不变式断言仪 (TypeScript)

无单点故障的分布式共识日志协议实现，附带全量状态离线完整性巡检仪与运行时不变式断言仪。
纯 TypeScript、零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 运行要求

- Node.js **>= 22.6**（使用内置 TypeScript 类型擦除；Node >= 23.6 可省略标志）

```bash
npm test          # node --experimental-strip-types --test test/*.test.ts
npm run demo      # node --experimental-strip-types src/demo.ts
```

## 组件

| 文件 | 职责 |
| --- | --- |
| `src/node.ts` | Multi-Paxos 节点：Prepare/Promise、Accept/Accepted 两阶段、领导者租约、空洞回填、哈希链日志 |
| `src/network.ts` | 确定性内存网络模拟器：异步投递、对称网络分区、愈合、消息统计 |
| `src/invariants.ts` | 不变式断言仪：选票单调性、已提交不可变、无空洞、租约互斥、哈希完整性、严格无泄漏 |
| `src/inspector.ts` | 离线完整性巡检：悬挂引用、孤立环路、数据倾斜、已提交冲突 |
| `src/cluster.ts` | 集群装配（节点 + 网络 + 手动时钟 + 监视器） |
| `src/demo.ts` | 验收场景运行样例 |

## 协议要点

- **Phase 1 (Prepare/Promise)**：提案者以单调递增选票征集多数派承诺；承诺携带各接受者已接受的全部条目。
- **Phase 2 (Accept/Accepted)**：逐槽位选择「最高选票的已接受值」（无则用自己的值），多数派确认后提交并广播 `Commit`。
- **领导者租约**：租约有效期内（`LEASE_MS`）的连续 `LogEntry` 直接走 Phase 2，跳过 Phase 1；租约过期或被更高选票抢占则退位。
- **无空洞提交**：新 Leader 在 Phase 1 后对 `[1, maxSlot]` 全部槽位回填（无已接受值的槽位填 `noop`），`commitIndex` 仅连续推进；落后者通过 `SyncRequest/SyncResponse` 拉取缺失的已提交前缀并修复哈希链。
- **少数派脏日志安全覆写**：已提交条目是事实源；未提交条目被多数派决议覆写，已提交值永不被改写（由断言仪强制）。

## 巡检仪检测项

- **悬挂引用**：`prevHash` 指向不存在的条目、前驱槽位缺失、slot 1 未锚定 GENESIS、`commitIndex` 越过已提交日志、租约指向未知节点。
- **孤立环路**：条目哈希链 / 租约引用图中的环，且从任何已提交根不可达。
- **数据倾斜**：日志规模偏离集群均值超阈值、已提交槽位副本数低于法定人数。
- **已提交冲突**：同一槽位在不同节点被提交为不同值（按值比较，允许同值不同选票）。

## 运行样例（`npm run demo`）

```
--- 5-node Multi-Paxos cluster, quorum = 3 ---

== N1 elected leader, two entries committed on majority ==
  N1 commitIndex=2 committed=[1:cmd(set x=1) 2:cmd(set y=2)] LEADER(lease)
  ...（全部 5 节点一致）

N1 (old leader, minority side) cannot commit: accept phase failed as expected

== partition {N1,N2} | {N3,N4,N5}; slot 3 dirty on minority only ==
  N1 commitIndex=2 ... uncommitted=[3:cmd(set z=3)] LEADER(lease)
  N2 commitIndex=2 ... uncommitted=[3:cmd(set z=3)]

== N4 won Phase 1 in majority partition, committed slot 3 ==
  N4 commitIndex=3 committed=[1:cmd(set x=1) 2:cmd(set y=2) 3:cmd(set w=4)] LEADER(lease)

== partition healed; minority dirty log safely overwritten ==
  N1..N5 commitIndex=4 committed=[1:cmd(set x=1) 2:cmd(set y=2) 3:cmd(set w=4) 4:cmd(set v=5)]

== offline integrity inspection ==
  ok=true dangling=0 orphanLoops=0 skew=0 conflicts=0
== invariant monitor: clean, no leaks ==
```

## 测试结果（`npm test`，36 项全部通过）

```
# tests 36
# pass 36
# fail 0
```

- `test/paxos.test.ts`（6）：选举与提交、租约跳过 Phase 1、租约过期重选、Phase 1 空洞 noop 回填、
  **验收场景**（5 节点分区 → 旧 Leader 被多数派新 Leader 取代 → Phase 1 补全未提交槽位 → 安全覆写少数派脏日志）、
  Leader 轮换下已提交值保持（法定人数相交）、并发竞速提案 + 随机分区/愈合/时钟推进下的安全性。
- `test/inspector.test.ts`（16）：干净/空快照、5 类悬挂引用、孤立环路、日志规模倾斜、副本不足倾斜、
  已提交冲突、同值异选票非冲突、真实收敛集群巡检。
- `test/invariants.test.ts`（14）：选票回退、已提交覆写、跨节点提交冲突、提交水位下空洞、
  租约互斥、泄漏严格断言、哈希篡改拒绝、过期选票 Accept 拒绝。

所有测试结束均执行 `monitor.assertNoLeaks()` + `monitor.assertClean()`：任何悬挂的
未决操作或不变式违例都会使测试失败。
