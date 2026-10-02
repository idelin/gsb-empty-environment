# 租约争抢事件可重放审计追踪器

用于排查分布式协调故障的事件记录黑匣子。将租约的**申请、续期、移交、超时、拒绝、竞态冲突**以及**仲裁器互斥锁状态变迁**完整记录为带 Lamport 逻辑时间戳的结构化流水，支持按时间轴回放租约争抢全过程，并对任意事件给出因果证据链。

纯 TypeScript 实现，零第三方依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 运行环境

- Node.js ≥ 23.6（原生 TypeScript 类型擦除，开箱即用）
- 或 Node.js ≥ 22.6，运行时加 `--experimental-strip-types`

```bash
nvm use            # 或自行切换到 node >= 23.6
npm test           # 运行全部内置测试
npm run demo       # 运行竞态复现样例
```

## 设计要点

- **到达次序可仲裁**：`LEASE_REQUESTED` 在进入临界区*之前*同步落账。两个请求即使落在同一物理毫秒、双方本地逻辑时钟相同，仲裁器观察次序（流水序号 + 仲裁器逻辑时钟）仍能确定性地还原先后。
- **逻辑时间戳**：Lamport 时钟（`src/logical-clock.ts`），排序键为 `(lamport, nodeId)` 全序；物理墙钟仅作参考，不作排序依据。
- **因果链**：每个事件通过 `causedBy` 引用直接导致它的事件序号，`AuditTrail.causalChain(seq)` 可回溯到根因；`verifyCausalIntegrity()` 校验引用完整性。
- **锁状态变迁**：仲裁器互斥锁的每次获取/释放都记录 `ARBITER_LOCK_ACQUIRED/RELEASED`，测试断言其严格交替（互斥证据）。
- **脑裂防护**：授予/移交时递增 `fencingToken`，过期 token 的续期会被 `FENCING_TOKEN_STALE` 拒绝。

## 代码结构

| 文件 | 职责 |
| --- | --- |
| `src/logical-clock.ts` | Lamport 逻辑时钟与全序比较 |
| `src/events.ts` | 9 类审计事件与负载类型定义 |
| `src/audit-trail.ts` | 只追加事件流水、时间轴回放、因果链追溯 |
| `src/arbiter.ts` | 租约仲裁器（申请/续期/移交/超时 + 互斥临界区） |
| `src/replay.ts` | 时间轴与因果链的可读化渲染 |
| `test/lease-contention.test.ts` | 竞态复现与验收测试（node:test） |
| `examples/race-demo.ts` | 竞态复现运行样例 |

## 运行样例（`npm run demo`）

复现 node-a / node-b 在**同一物理毫秒**同时争抢主租约：仲裁器临界区被撑开 5ms，第二个请求在窗口内到达。

```text
[t=1760000000000ms] node-a 与 node-b 同时申请主租约 "master"...

—— 仲裁结果 ——
node-a: 胜出，fencingToken=1
node-b: 被拒，reason=LEASE_HELD

=== 租约争抢时间轴回放（lease=master） ===
#01 [LEASE_REQUESTED] L(2@arbiter) wall=1760000000000ms 节点 node-a 申请租约（请求方时钟 L(1@node-a)，ttl=30000ms）
#02 [LEASE_REQUESTED] L(3@arbiter) wall=1760000000000ms 节点 node-b 申请租约（请求方时钟 L(1@node-b)，ttl=30000ms）
#03 [ARBITER_LOCK_ACQUIRED] L(4@arbiter) wall=1760000000000ms 仲裁器锁 IDLE→LOCKED（操作=acquire:master） 因果<=#1
#04 [LEASE_GRANTED] L(5@arbiter) wall=1760000000000ms 租约授予 node-a（fencingToken=1，到期@1760000030000） 因果<=#1
#05 [ARBITER_LOCK_RELEASED] L(6@arbiter) wall=1760000000000ms 仲裁器锁 LOCKED→IDLE（操作=acquire:master）
#06 [ARBITER_LOCK_ACQUIRED] L(7@arbiter) wall=1760000000000ms 仲裁器锁 IDLE→LOCKED（操作=acquire:master） 因果<=#2
#07 [CONTENTION_DETECTED] L(8@arbiter) wall=1760000000000ms 检测到竞态冲突：node-b(req#2) 争抢 node-a(req#1) 持有的租约，仲裁器锁=LOCKED，同一毫秒到达=true 因果<=#2
#08 [LEASE_REJECTED] L(9@arbiter) wall=1760000000000ms 节点 node-b 被拒绝（原因=LEASE_HELD，当前持有者=node-a） 因果<=#7,#2
#09 [ARBITER_LOCK_RELEASED] L(10@arbiter) wall=1760000000000ms 仲裁器锁 LOCKED→IDLE（操作=acquire:master）

=== 事件 #8 的因果证据链 ===
根因 #02 [LEASE_REQUESTED] ... 节点 node-b 申请租约
  导致 #07 [CONTENTION_DETECTED] ... node-b(req#2) 争抢 node-a(req#1)，同一毫秒到达=true
    导致 #08 [LEASE_REJECTED] ... node-b 被拒绝（原因=LEASE_HELD，当前持有者=node-a）

因果引用完整性校验: 通过
```

证据链解读：`#01/#02` 证明两份请求落在同一毫秒且双方本地时钟相同（各为 `L(1@node-x)`），只有仲裁器观察次序可定序；`#03~#05` 证明 node-a 的临界区先完成授予；`#07` 证明 node-b 进入临界区时锁已被重新获取、租约已被持有；`#08` 的拒绝响应因果关联到冲突事件与落败请求。

## 测试结果（`npm test`）

```text
✔ 竞态复现：同一毫秒双请求，先到者胜出、后来者被拒
✔ 仲裁器锁状态变迁完整且互斥（无嵌套获取）
✔ 竞态冲突事件与拒绝响应构成完整证据链
✔ 时间轴回放：逻辑时钟全序与记录次序一致
✔ 完整生命周期：申请→续期→移交→超时 全部落账
✔ 防护性拒绝：非持有者续期与过期 fencing token
ℹ tests 6  ℹ pass 6  ℹ fail 0
```
