# constrained-topo

带固定位置约束的拓扑排序器（TypeScript，零依赖，Node >= 22 原生类型擦除直接运行）。

## 问题定义

给定 `n` 个节点（编号 `0..n-1`）、先后关系边集 `edges: [u, v][]`（u 必须在 v 之前），
以及若干节点的固定位置 `fixed: Map<node, position>`（位置从 **0** 计，每个节点至多一个固定位置，
每个位置至多一个节点），求同时满足两类约束的**字典序最小**排列。

## 三态结果

```ts
import { solveConstrainedTopo, verifyEvidence } from "./src/index.ts";

const result = solveConstrainedTopo(n, edges, fixed, { budget: 100_000 });
// { status: "solved",     permutation, explored }  —— 找到字典序最小的合法排列
// { status: "infeasible", evidence,    explored }  —— 不可行，附可验证证据
// { status: "unknown",                 explored }  —— 搜索预算耗尽，无法判定
```

关键原则：**普通拓扑排序成功不代表存在位置解**。只有搜索真正构造出满足全部
固定位置的完整排列时才返回 `solved`；`unknown` 不伪造任何结论。

## 证据（可独立验证）

`verifyEvidence(n, edges, fixed, evidence)` 用与求解器不同的独立实现做确定性验证：

| 证据 | 含义 |
| --- | --- |
| `fixed-position-out-of-range` | 固定位置越出 `[0, n)` |
| `duplicate-fixed-position` | 两个节点占用同一位置 |
| `cycle` | 先后关系成环（含自环） |
| `chain-contradiction` | 依赖链 `u⇝…⇝v` 长 `k`，但 `f(u)+k > f(v)` |
| `interval-squeeze` | 超过 `t-s` 个节点被挤在区间 `[s, t)` 内（Hall 条件；前缀/后缀/空区间是特例） |
| `exhaustive-search` | 兜底：搜索空间在预算内穷尽仍无解，验证器用独立穷尽判定复核 |

区间挤占证据中每个成员节点都附下界/上界证明（固定位置本身，或到固定节点的依赖链路径），
验证器逐条核对边、位置与计数。

## 预算与未知状态

`options.budget` 限制回溯搜索的节点数（默认 `100_000`，可传 `Infinity`）。
预算耗尽时返回 `unknown`——结果不可判定，而非"无解"。

## 验证（测试与压力核对）

```sh
npm test        # node --test：单元测试 + 与 ≤9 点排列枚举全量比对
npm run stress  # 2.4 万随机实例（n≤9）与枚举预言机交叉核对
```

测试覆盖：空图、环/自环、固定位置被依赖链挤占（拓扑有序但无位置解）、
中间区间挤占（前后缀检查无法命中的深层冲突）、字典序最小性、预算耗尽语义，
以及随机实例与 `lexMinValidPermutation` 预言机的全量一致性
（solved 时排列必须等于字典序最小合法排列，infeasible 时证据必须可验证）。

## 结构

- `src/solver.ts` —— 结构检查（环、链矛盾、区间挤占）+ 预算内字典序回溯搜索
- `src/evidence.ts` —— 证据类型与独立验证器
- `src/bruteForce.ts` —— ≤9 点排列枚举预言机 + 独立穷尽判定
- `test/solver.test.ts` —— 单元测试与随机交叉验证
- `scripts/stress.ts` —— 大规模随机压力核对
