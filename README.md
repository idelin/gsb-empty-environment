# Incremental Max-Flow & Min-Cut Certificate Generator

有向网络整数容量的**增量最大流动态调整器**，每次容量变更后输出新的最大流值与
S-T 最小割证书（割边集合 + 节点可达性划分）。纯 TypeScript，零依赖，使用
Node.js 内置测试运行器（`node:test`）验证。

## 功能

- `addEdge(u, v, capacity)` 构建有向整数容量网络；
- `computeMaxFlow()` 以 Dinic 算法计算初始最大流并输出割证书；
- `updateCapacities(changes)` **批量**调整已有边容量（增/减）：
  - 容量缩减导致当前流量超限时，沿残余网络**反向增广轨撤回**超限流量
    （将多余流量沿反向弧推回源点、从汇点回补缺口，恢复流量守恒）；
  - 随后在残余网络中重新寻找替代增广路径，尽量恢复流量；
  - 返回撤回量、恢复量、新最大流值与最小割证书；
- 割证书通过残余网络 BFS 可达性生成：源侧集合 `S`、汇侧集合 `T`、
  跨越 S→T 的割边集合及其容量和（满足最大流最小割对偶：割容量 = 最大流）。

## 运行环境

Node.js ≥ 22.6（使用内置 TypeScript type-stripping 与 `node:test`，无需安装任何依赖）。

```bash
npm run demo   # 运行验收样例
npm test       # 运行内置测试
```

## 验收场景（`npm run demo`）

网络：`s→a:5, s→b:5, a→t:5(关键边), b→t:6, a→b:2`，初始最大流 10。
将关键边 `a→t` 容量从 5 降至 2：撤回 3 单位流量，经替代路径
`s→a→b→t` 恢复 1 单位，最终流量 8。

实际输出：

```
=== initial network ===
max flow:        10
source side (S): { s }
sink side (T):   { t, a, b }
cut edges (S->T):
  s -> a  capacity=5 flow=5
  s -> b  capacity=5 flow=5
cut capacity:    10

=== capacity change: a -> t reduced 5 -> 2 ===
flow withdrawn along reverse tracks: 3
flow recovered via alternative paths: 1

=== after adjustment ===
max flow:        8
source side (S): { s, a, b }
sink side (T):   { t }
cut edges (S->T):
  a -> t  capacity=2 flow=2
  b -> t  capacity=6 flow=6
cut capacity:    8

final edge flows:
  s -> a  flow=3/5
  s -> b  flow=5/5
  a -> t  flow=2/2
  b -> t  flow=6/6
  a -> b  flow=1/2
```

## 测试结果（`npm test`）

7 个用例全部通过：

```
ok 1 - initial max flow of the network is 10
ok 2 - reducing key edge 5 -> 2 withdraws 3 units and readjusts flow to 8
ok 3 - capacity decrease that stays feasible does not withdraw flow
ok 4 - capacity increase re-augments flow along newly available capacity
ok 5 - batch of mixed increases and decreases is applied atomically
ok 6 - sequential updates stay consistent with a from-scratch recomputation
ok 7 - rejects invalid capacities and unknown edges
# pass 7 / fail 0
```

测试除断言验收数值外，还独立校验：容量约束、节点流量守恒、
割证书合法性（割边端点分属两侧、割容量 = 最大流）。

## 文件结构

- `src/maxflow.ts` — 增量最大流调整器与割证书生成
- `src/demo.ts` — 验收场景运行样例
- `test/maxflow.test.ts` — Node.js 内置测试
