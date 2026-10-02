# incremental-maxflow

有向网络整数容量最大流动态调整器（TypeScript）。支持批量增加 / 减少已有边的容量；
容量缩减导致当前流量超限时，沿残余网络执行反向流撤回（reverse flow withdrawal），
随后重新寻找替代增广路径恢复最大流。每次变更输出新的最大流量值与 S-T 最小割证书
（节点可达性划分 + 割边集合）。

## 环境要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译步骤）
- 可选：安装 `typescript` 后运行 `npm run typecheck` 做类型检查

## 使用

```bash
npm test    # Node.js 内置测试运行器 (node:test)
npm run demo
```

```ts
import { IncrementalMaxFlow } from "./src/incremental-max-flow.ts";

const mf = new IncrementalMaxFlow(5, /*source*/ 0, /*sink*/ 4);
mf.addEdge(0, 1, 5);            // #0 s->a
mf.addEdge(0, 2, 5);            // #1 s->b
const key = mf.addEdge(1, 2, 5); // #2 a->b（关键边）
mf.addEdge(1, 3, 1);            // #3 a->d（替代出口）
mf.addEdge(3, 4, 1);            // #4 d->t
mf.addEdge(2, 4, 10);           // #5 b->t

mf.computeMaxFlow();            // { maxFlow: 10, ... }
mf.decreaseCapacity(key, 3);    // 关键边 5 -> 2，撤回 3 单位，恢复 1 单位 -> 8
mf.applyBatch([                 // 批量变更，每个变更返回一份证书
  { edgeId: key, newCapacity: 0 },
  { edgeId: key, newCapacity: 5 },
]);
```

## API

- `addEdge(from, to, capacity): number` — 添加有向边，返回边 id
- `computeMaxFlow(): MinCutCertificate` — 从零计算最大流（Edmonds-Karp）
- `increaseCapacity(edgeId, delta)` / `decreaseCapacity(edgeId, delta)` — 增量调整
- `setCapacity(edgeId, newCapacity)` — 设置绝对容量
- `applyBatch(changes): MinCutCertificate[]` — 批量变更，逐条返回证书
- `certificate(): MinCutCertificate` — 当前流的 S-T 最小割证书：
  `maxFlow`、`sourceSide`（残余网络中 s 可达集）、`sinkSide`、
  `cutEdges`（S→T 饱和边集合）、`cutCapacity`（恒等于 `maxFlow`）

## 算法

容量缩减使边 `e = (u, v)` 的流量超限时，设超限 `excess = flow(e) - cap(e)`：

1. 将 `flow(e)` 钳制到新容量，`u` 出现盈余、`v` 出现亏空；
2. 沿残余网络把 `excess` 单位从 `u` 推回源点 `s`、从汇点 `t` 推回 `v`
   （反向增广撤回），恢复流量守恒，流值减 `excess`；
3. 在残余网络上重新寻找 `s-t` 增广路径（替代路径）恢复最大流。

## 运行样例（`npm run demo`）

```
=== initial max flow ===
max flow value : 10
S side (reachable from s) : { s }
T side                    : { a, b, d, t }
cut edges (S -> T, saturated):
  edge#0  s -> a  capacity=5 flow=5
  edge#1  s -> b  capacity=5 flow=5
cut capacity   : 10  (== max flow, certificate valid)

>>> key edge #2 (a -> b): capacity 5 -> 2
    excess 3 units withdrawn along the residual network,
    then alternative path s -> a -> d -> t recovers 1 unit.

=== after decreasing key edge to 2 ===
max flow value : 8
S side (reachable from s) : { s, a }
T side                    : { b, d, t }
cut edges (S -> T, saturated):
  edge#1  s -> b  capacity=5 flow=5
  edge#2  a -> b  capacity=2 flow=2
  edge#3  a -> d  capacity=1 flow=1
cut capacity   : 8  (== max flow, certificate valid)
```

## 测试结果（`npm test`，node:test，7 个用例全部通过）

```
✔ initial max flow is 10 with a valid min-cut certificate
✔ reducing key edge 5 -> 2 withdraws 3 units and recovers 1 via alternative path
✔ capacity decrease below current flow value is a no-op for the flow
✔ increasing capacity restores flow along the freed edge
✔ batched capacity changes emit one certificate per change
✔ reducing the key edge to 0 drops the flow to 6
✔ invalid inputs are rejected
ℹ tests 7  ℹ pass 7  ℹ fail 0
```
