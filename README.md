# 租约争抢事件可重放审计追踪器 (Lease Contention Audit Trail)

用于排查分布式协调故障的事件记录黑匣子。租约的**申请、续期、移交、释放、
超时、拒绝、竞态冲突**全部记录为带 **Lamport 逻辑时间戳** 的结构化事件流水，
支持按时间轴完整回放租约争抢过程，并为每一次拒绝提供**因果关系证据链**。

## 运行环境

- Node.js **>= 23.6**（原生 TypeScript 类型擦除 + 内置 `node:test`，零依赖）
- 仓库内含 `.nvmrc`：`nvm use` 即可切换到合适版本

```bash
nvm use
npm test        # 运行全部内置测试
npm run demo    # 复现毫秒级主租约争抢竞态并回放审计追踪
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/logicalClock.ts` | Lamport 逻辑时钟（tick / merge） |
| `src/events.ts` | 9 类结构化事件与因果边 (`causedBy`) 定义 |
| `src/auditTrail.ts` | 只追加黑匣子：记录、查询、因果链、时间轴回放 |
| `src/arbiter.ts` | 租约仲裁器：到达即记录 + 串行处理 + 锁状态机 + 竞态检测 |
| `src/leaseNode.ts` | 争抢租约的分布式节点（各自持有逻辑时钟） |
| `src/raceSimulation.ts` | 复现两节点毫秒级同时争抢主租约的竞态漏洞 |

关键设计：请求**到达仲裁器的瞬间**即入审计流水（保留真实到达次序），
随后仲裁器从收件箱串行处理——这与真实网络中"两个请求都已到达、
仲裁器逐个处理"的竞态场景完全同构。

## 竞态复现与回放样例（`npm run demo`）

node-A 与 node-B 在 **0.42ms** 内同时申请 `master` 租约：

```text
=== Lease contention race replay (audit trail) ===

[L#  2 t+   0.000ms] REQUEST    node-A -> arbiter op=acquire lease=master ttl=5000ms
[L#  3 t+   0.420ms] REQUEST    node-B -> arbiter op=acquire lease=master ttl=5000ms
[L#  4 t+   0.420ms] RACE!     lease=master contenders=node-A vs node-B arrival gap=0.420ms
[L#  6 t+   0.520ms] GRANT     lease=master holder=node-A expires@+5000ms (version 1)
[L#  7 t+   0.520ms] LOCK      lease=master UNLOCKED -> LOCKED (grant, holder=node-A)
[L#  9 t+   0.520ms] REJECT     node-B op=acquire lease=master reason=LOCK_HELD (lock held by node-A)

=== Verdict ===
winner: node-A   loser: node-B
rejection: evt-6 reason=LOCK_HELD

=== Causal evidence chain for the rejection ===
evt-1 L#2 LEASE_REQUESTED (node-A)
evt-2 L#3 LEASE_REQUESTED (node-B)
evt-4 L#6 LEASE_GRANTED (node-A)  <- caused by [evt-1]
evt-6 L#9 LEASE_REJECTED (node-B)  <- caused by [evt-2, evt-4]
```

审计追踪完整还原了验收边界要求的三类证据：

1. **到达次序**：node-A 请求先于 node-B 0.42ms 到达（`LEASE_REQUESTED` × 2 + `CONTENTION_DETECTED`）；
2. **锁状态变迁**：`LOCK_STATE_CHANGED UNLOCKED -> LOCKED (grant, holder=node-A)`；
3. **拒绝响应与因果链**：`LEASE_REJECTED(node-B)` 的 `causedBy = [evt-2, evt-4]`，
   即"自己的请求" + "获胜者的授权"——`trail.causalChain(evt-6)` 可机械地还原整条证据链。

## 测试结果（`npm test`，Node v23.9.0）

```text
✔ grant then reject while lock is held
✔ renew extends expiry; non-holder renew is rejected
✔ transfer moves the lease; old holder can no longer renew
✔ lease expires after TTL and can be re-acquired
✔ release frees the lock
✔ lamport timestamps are monotonic across the trail
✔ tick is strictly monotonic
✔ merge takes max(local, remote) + 1
✔ millisecond race: full reconstruction from the audit trail
✔ race outcome is deterministic: arrival order decides the winner
✔ requests outside the contention window do not raise a race flag
✔ replay orders events by logical time and renders a timeline
✔ causal chain of the rejection is a self-contained evidence path
✔ trail rejects events with unknown causal parents
ℹ tests 14
ℹ pass 14
ℹ fail 0
```

其中 `test/race.test.ts` 的 "millisecond race" 用例即验收边界：
断言到达次序、竞态标记（gap=0.42ms）、锁状态机仅变迁一次、
先到者胜出 / 后到者被 `LOCK_HELD` 拒绝，以及拒绝事件的因果证据链完整闭合。
