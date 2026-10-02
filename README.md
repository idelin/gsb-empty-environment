# 因果广播（Causal Broadcast）可靠中间件

基于**向量时钟**的分布式消息层，保证因果顺序交付：

- **发送端**：每次广播前推进本地向量时钟，并将时钟快照附加到消息上（即该消息的全部因果依赖）。
- **接收端**：仅当消息的所有前置因果依赖均已交付时才递交给应用层；否则将消息暂存于**隔离队列**（hold-back queue），待依赖补齐后**自动级联交付**，杜绝因果倒置。

## 可交付判定条件

消息 `m`（发送者 `s`，附加时钟 `V`）在节点 `p`（本地时钟 `L`）可交付，当且仅当：

1. `V[s] == L[s] + 1` —— 它是发送者序列中的下一条消息；
2. 对所有 `k ≠ s`：`V[k] <= L[k]` —— `m` 因果依赖的所有消息均已在 `p` 交付。

## 目录结构

```
src/vectorClock.ts      向量时钟：tick / merge / happened-before 比较
src/causalBroadcast.ts  中间件核心：广播、接收、隔离队列、级联交付
src/demo.ts             运行样例：模拟网络抖动下 M1 先于 M0 到达
test/causalBroadcast.test.ts  Node.js 内置测试（node:test）
```

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤；
Node ≥ 23.6 默认启用，更低版本可加 `--experimental-strip-types`）。

## 运行

```bash
npm test     # 运行全部测试
npm run demo # 运行乱序交付演示
```

## 运行样例输出（`npm run demo`）

场景：节点 A 连续广播 M0、M1（M1 因果依赖 M0）；抖动网络让 M1（20ms）先于 M0（300ms）到达接收端 B。

```
== 节点 A 连续广播 M0、M1（M1 因果依赖 M0）==

== 50ms 时：M1 已先到，但被隔离缓存 ==
  B 隔离队列长度 = 1（M1 等待 M0）
  B 已交付序列 = []（为空，无因果倒置）
  [B 应用层] 交付 A#1  内容="M0: 第一条消息"  时钟=[A:1]
  [B 应用层] 交付 A#2  内容="M1: 依赖 M0 的回复"  时钟=[A:2]

== 350ms 时：M0 到达，触发级联交付 ==
  B 隔离队列长度 = 0
  B 最终交付序列 = [A#1, A#2]

✔ 因果顺序保持：M0 先于 M1 交付，无因果倒置
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ 验收边界：M1 先于 M0 到达时被隔离缓存，M0 到达后自动级联交付
✔ 顺序到达的消息立即交付，不进入隔离队列
✔ 无因果关系的并发消息可立即交付
✔ 跨节点因果依赖：B 的消息依赖 A 的消息，乱序时同样被缓存
✔ 长依赖链整体逆序到达时按因果序级联交付
✔ 重复投递的消息被去重，只交付一次
✔ 发送端本地广播立即交付给本地应用层
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

## 用法示例

```ts
import { CausalBroadcast } from "./src/causalBroadcast.ts";

const node = new CausalBroadcast("A");
node.setTransport((msg) => network.send(msg));   // 接入任意传输层
node.onDeliver((msg) => app.handle(msg.payload)); // 应用层只收到因果有序的消息
node.broadcast("hello");
```
