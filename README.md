# multi-paxos-core

Multi-Paxos 多数派共识与无空洞日志提交引擎 —— 基础协议状态机与核心语义实现（TypeScript，零依赖）。

## 核心语义

- **两阶段协议**：Phase 1 `Prepare/Promise`（含日志修复信息上报）+ Phase 2 `Accept/Accepted`。
- **领导者租约**：租约有效期内跳过 Phase 1，直接以 Phase 2 连续追加 `LogEntry`；租约到期自动重新执行 Phase 1（`src/node.ts` 中 `LEASE_DURATION`）。
- **无空洞提交**：`commitIndex` 仅覆盖连续前缀；Leader 仅在多数派接受连续槽位后推进；Follower 仅提交本地连续前缀，绝不跨洞。
- **空洞修补**：新 Leader 的 Phase 1 收集多数派 Promise 中的已接受日志，对每个已知槽位选择最高选票的条目重新提议，缺失槽位以 `NOOP` 填充，全部以新选票重放后方可服务客户端流量。
- **脏日志覆写**：少数派上未提交的旧值在重新当选后的 Phase 1/Phase 2 中被多数派已选值安全覆写；已提交值由法定人数交集保证永不丢失。

## 核心不变量（实现即断言）

1. **Promise 单调**：`promised` 选票永不回退；拒绝一切更低选票的 Prepare/Accept。
2. **提交无洞**：任何节点 `commitIndex` 只覆盖 `1..commitIndex` 连续前缀。
3. **Leader 完整性**：新 Leader 先修复 `1..maxSlot` 再服务客户端；已提交值必被后续任何 Leader 选择。
4. **确定性时钟**：单线程事件队列按 `(time, seq)` 全序处理，逻辑时钟为唯一时间源；同脚本同种子 => 逐比特相同的消息轨迹与终态。

## 目录结构

- `src/types.ts` — Ballot 全序、日志条目、五类协议消息
- `src/node.ts` — 协议状态机（角色/租约/两阶段/修复/提交推进）
- `src/cluster.ts` — 确定性集群模拟器（分区/隔离/抖动延迟/安全断言）
- `test/` — Node 内置测试（`node:test`）：选票序、Phase 1/2 边界、租约、修复、分区验收场景、对抗性交错模糊测试
- `examples/demo.ts` — 验收场景运行样例

## 运行要求

- Node.js ≥ 23.6（原生运行 TypeScript，内置 `node:test`）。
- Node 22.6–23.5 需加 `--experimental-strip-types`。
- 可选类型检查：联网后 `npm i -D typescript @types/node && npx tsc --noEmit`（源码仅使用可擦除类型语法，`tsconfig.json` 已启用 `erasableSyntaxOnly`）。

```bash
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 测试结果（Node v24.19.0）

```
ℹ tests 35
ℹ pass 35
ℹ fail 0
```

连续 3 次全量运行结果一致（确定性模拟，无 flake）。覆盖：Ballot 全序边界、Prepare/Promise 承诺与拒绝、Promise 单调性、多数派当选精确边界、nack 携高选票退位、过期选票忽略、Accept 幂等/覆写/拒绝、提交前沿不跨洞、Commit 通知单调、租约快路径/到期/精确边界、NOOP 填洞、已提交值跨选举保持、脏值覆写、5 节点网络分区验收场景、隔离节点、双 Leader 竞争、多种子对抗性交错（每 tick 断言安全）、逐比特确定性复现、快速换主下单一提交前缀。

## 运行样例（`npm run demo` 摘要）

```
=== 2. partition {0,1}|{2,3,4}; old leader cannot commit "set y=2" (t=7) ===
  node0  leader    commit=1  committed=[set x=1]  log={1:set x=1 2:set y=2}   <- 少数派脏数据
  node2  follower  commit=1  committed=[set x=1]  log={1:set x=1}
=== 3. majority elects node2; Phase 1 repair; "set z=3" committed (t=14) ===
  node2  leader    commit=2  committed=[set x=1, set z=3]
=== 5. healed; re-election overwrites dirty log; all nodes converge (t=80) ===
  node0  follower  commit=2  committed=[set x=1, set z=3]  log={1:set x=1 2:set z=3}  <- 脏值被覆写
  ...全部 5 节点一致
Final committed prefix on every node: ["set x=1", "set z=3"]
Safety held: dirty "set y=2"/"set w=4" never committed; pending events: 0
```
