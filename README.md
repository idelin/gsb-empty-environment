# Causal Broadcast Middleware

可靠因果广播中间件（TypeScript，零依赖）：发送端为消息附加本地向量时钟；
接收端缓存乱序到达的消息，仅当某消息的全部因果前置依赖都已交付后，
才将其递交应用层，从而杜绝因果倒置。

## 环境要求

- Node.js >= 22.6（利用原生 type stripping 直接运行 `.ts`，无需编译步骤；
  开发验证使用 Node v23.9.0）

## 运行

```bash
npm test    # Node.js 内置测试运行器 (node:test)
npm run demo
```

## 核心机制

- `src/vectorClock.ts` — 向量时钟工具与交付判定规则 `isDeliverable`：
  来自 `s` 的消息可交付，当且仅当
  1. `msg.clock[s] == local[s] + 1`（是该发送者的下一条消息），且
  2. 对所有 `k != s`：`msg.clock[k] <= local[k]`（发送时的全部因果前驱已交付）。
- `src/causalBroadcast.ts` — `CausalBroadcast` 类：
  - `broadcast(payload)`：递增本地时钟、加盖向量时间戳、本地回环交付，
    返回待传输的消息；
  - `receive(msg)`：满足交付规则则立即交付，否则放入隔离缓冲区；
    每次交付后级联扫描缓冲区，交付所有依赖已满足的消息；
  - 自动去重（重复到达的消息直接丢弃）。

## 验收场景

M1 因果依赖 M0，网络抖动使 M1 先到达接收端：中间件检测到 M0 未到，
将 M1 暂存于隔离队列；M0 到达并交付后，自动级联交付 M1。
对应测试：`test/causalBroadcast.test.ts` 中的 acceptance 用例，
以及 `examples/demo.ts` 的 Scenario 1。

## 测试结果

```
▶ causal broadcast middleware
  ✔ acceptance: M1 depends on M0, M1 arrives first, parked, then cascade-delivered after M0
  ✔ delivers in-order messages immediately without buffering
  ✔ delivers concurrent (independent) messages in any arrival order
  ✔ drains a reversed chain M2,M1,M0 into causal order
  ✔ ignores duplicate deliveries
  ✔ propagates causal dependencies across three nodes
  ✔ broadcast loopback delivers locally and stamps vector timestamps
  ✔ merges vector clocks on delivery
ℹ tests 8  pass 8  fail 0
```

## 样例输出（npm run demo）

```
=== Scenario 1: out-of-order arrival (M1 before M0) ===
  [network] jitter! M1 reaches bob first
  [bob] M1 depends on M0 which is missing -> parked (buffered=1, delivered=0)
  [network] M0 finally arrives
  [bob/app] delivered "M0: deploy schema" (from alice, clock={"alice":1})
  [bob/app] delivered "M1: insert row" (from alice, clock={"alice":2})
  [bob] cascade drained buffer (buffered=0)
  => delivery order at bob: ["M0: deploy schema","M1: insert row"]

=== Scenario 2: 3 nodes, random network jitter ===
  [A/app] delivery order: ["A:a1","A:a2","C:c1-after-a1","B:b1"]
  [B/app] delivery order: ["B:b1","A:a1","A:a2","C:c1-after-a1"]
  [C/app] delivery order: ["A:a1","C:c1-after-a1","B:b1","A:a2"]
  => causal order preserved at all nodes
```

## 用法示例

```ts
import { CausalBroadcast } from "./src/causalBroadcast.ts";

const alice = new CausalBroadcast<string>("alice");
const bob = new CausalBroadcast<string>("bob", (msg) => {
  console.log("delivered:", msg.payload);
});

const m0 = alice.broadcast("M0");
const m1 = alice.broadcast("M1"); // 因果依赖 M0

bob.receive(m1); // 乱序：M1 先到 -> 暂存，不交付
bob.receive(m0); // M0 到达 -> 交付 M0，随后级联交付 M1
```
