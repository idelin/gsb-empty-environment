# Dynamic Dominator Tree

控制流图（CFG）支配树计算器，TypeScript 实现，零依赖，使用 Node.js 内置
测试运行器（`node:test`）验证。

## 功能

- **支配关系计算**：从入口节点出发，用经典数据流方程迭代求不动点：
  `Dom(entry) = {entry}`，`Dom(n) = {n} ∪ ⋂ Dom(p)`（p 为 n 的前驱），
  并由支配集导出每个节点的直接支配者（idom）。
- **增量局部更新**：`addEdge(u, v)` / `removeEdge(u, v)` 只重算受影响区域
  （`{v} ∪ descendants(v)`——任何新增/消失的 entry→w 路径必经过 v），
  区域外节点的支配集作为固定边界值参与局部不动点迭代。
- **反例生成器**：`counterexample(a, b)` 对「a 支配 b」的否定给出见证——
  一条从入口到 b 且绕过 a 的真实路径（BFS）；若 a 确实支配 b 则返回
  `null`。`isValidWitness` 可校验路径合法性。

## 运行要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

```sh
npm test    # node --test
npm run demo
```

## 验收场景（examples/demo.ts 输出）

```
=== 1. Diamond CFG: S -> {A, B} -> T ===
  idom(S)=∅  idom(A)=S  idom(B)=S  idom(T)=S
  "A dominates T"? NO — counterexample path: S → B → T
=== 2. Add bypass edge S -> T (incremental) ===
  idom(T)=S   （绕行边不改变 T 的直接支配者）
=== 3. Remove edge B -> T (incremental) ===
  idom(T)=S   （S->T 仍在）
=== 4. Remove bypass S -> T as well ===
  idom(T)=A   （T 的直接支配者跃迁为 A）
  "A dominates T"? YES — no avoiding path exists
```

纯菱形结构（无绕行边）中删除 `B->T` 后 `idom(T)` 直接跃迁为 `A`，
见 `test/dominator.test.ts` 的
`incremental remove: deleting B->T shifts idom(T) to A`。

## 测试结果

```
✔ diamond: idom(T) is the fork node S
✔ diamond: neither A nor B dominates T
✔ incremental add: bypass edge S->T keeps idom(T) = S
✔ incremental remove: deleting B->T shifts idom(T) to A
✔ counterexample: path to T avoiding A in the diamond
✔ counterexample: none exists once A truly dominates T
✔ counterexample: bypass edge S->T yields direct witness
✔ full acceptance sequence: add bypass then delete B->T
✔ unreachable nodes have no dominators and no counterexample
✔ linear chain: each node is idom of the next
✔ incremental result matches full recomputation from scratch
ℹ tests 11  ℹ pass 11  ℹ fail 0
```

## 文件结构

- `src/dominator.ts` — `DominatorTree` 核心实现
- `test/dominator.test.ts` — `node:test` + `node:assert` 测试
- `examples/demo.ts` — 验收场景运行样例
