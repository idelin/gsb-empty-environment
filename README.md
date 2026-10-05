# constrained-topo-sort

拓扑排序器：在处理先后关系（边）之外，同时满足**固定位置约束**（pin），
返回**字典序最小**的合法排列；无解时给出**可独立验证的证据**；搜索超出
预算时返回 **unknown**，绝不把普通拓扑排序的成功误判为有位置解。

## 问题定义

- 节点为 `0..n-1`，位置从 0 计。
- `edges: [before, after][]` 表示先后关系。
- `pins: Map<node, position>` 每个节点最多一个固定位置。
- 合法排列：满足所有边，且每个 pinned 节点恰好位于其固定位置。
- 字典序：按排列中节点编号的逐元素比较。

## API（`src/constrainedTopoSort.ts`）

```ts
solve(n, edges, pins, budget = 1_000_000): SolveResult
```

- `{ status: 'ok', order, stats }` — 字典序最小的合法排列。
- `{ status: 'infeasible', evidence, stats }` — 附可验证证据：
  - `cycle`：环上的节点列表（相邻含回边均为原图的边）；
  - `duplicate-pin-position`：同一位置上的全部节点；
  - `pin-out-of-range`：越界的固定位置；
  - `ancestors-exceed-slot` / `descendants-exceed-slot`：依赖链把固定位置
    挤占（祖先数 > 位置，或后继数 > 右侧槽位数），证据列出全部祖先/后继；
  - `pinned-order-contradiction`：祖先的固定位置不早于后继，附见证链；
  - `exhaustive-search`：结构性检查均未触发，由预算内穷举搜索证明无解
    （可通过重跑搜索或排列枚举复核）。
- `{ status: 'unknown', reason: 'budget-exhausted', stats }` — 预算耗尽，
  不猜测结论。

## 设计要点

- 普通拓扑排序成功只说明无环，**不代表**位置约束可满足；求解器先做
  结构性证据检查，再做带预算的 DFS（候选按编号升序，首个完整解即字典
  序最小），pinned 节点只允许落在自己的位置上。
- 前向剪枝：未放置的 pinned 节点的未放置祖先必须还能塞进其位置之前。
- 默认预算 1,000,000 大于 n=9 的排列搜索树节点总数（986,410），因此
  n ≤ 9 的实例在默认预算下必然给出确定结论，可与枚举互相核对。

## 验证（`src/enumerate.ts`）

- `isValidOrder`：校验一个排列是否满足全部边与 pin。
- `enumerateValidOrders`：n ≤ 9 时枚举全部排列，给出合法解数量与字典
  序最小解。
- `verifyEvidence`：仅依据原始输入独立复核 infeasible 证据。

测试（`test/runTests.ts`）覆盖：空图、环、依赖链挤占固定位置（含结构性
与纯搜索两个层面）、pin 冲突、预算耗尽的 unknown、n=9 枚举核对，以及
200 个随机实例与枚举的交叉验证。

## 运行

```sh
npm test     # 擦除类型到 build/ 并运行全部测试
npm run build
```

源码使用可擦除类型的 TypeScript 子集（无 enum/class/泛型实参/`as`），
由 `tools/stripTypes.mjs` 擦除类型后在 Node 上运行；`tsconfig.json`
供装有 tsc 的环境做 `strict` 类型检查。
