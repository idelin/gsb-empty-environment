# Dynamic Dominator Tree

控制流图支配关系计算器：从入口节点计算所有节点的直接支配者（idom），
支持单条控制流边的增量插入/删除与局部更新，并能对"不支配"关系生成
反例证明路径（一条从入口到目标、绕开候选支配者的具体路径）。

## 环境要求

- Node.js >= 23.6（原生 TypeScript 类型剥离，无需编译步骤；源码仅使用可擦除类型语法）
- 可选：若装有 `tsc`，`npm run typecheck` 可做严格类型检查

## 运行

```sh
npm test        # Node.js 内置测试运行器 (node:test)
npm run demo    # 验收场景运行样例
```

## API（src/dominator.ts）

- `new DominatorTree(entry, edges?)` — 构建并计算支配树
- `tree.idom(n)` — 节点 `n` 的直接支配者（入口或不可达时返回 `null`）
- `tree.dominates(d, n)` / `tree.dominatorsOf(n)` — 支配关系查询
- `tree.addEdge(u, v)` / `tree.removeEdge(u, v)` — 增量局部更新支配树
- `tree.proveNotDominates(d, t)` — 若 `d` 不支配 `t`，返回绕开 `d` 的
  入口→目标路径作为反例；否则返回 `null`

## 增量更新策略

基于热启动数据流迭代的局部更新：

- **插边**只会增加路径，支配集合只会缩小，当前解即为合法种子，直接迭代至不动点；
- **删边** `u->v` 只会删除路径，支配集合只会增大，且受影响区域被限定为当前支配树中
  `v` 的子树；将该区域重置为"全部可达节点"后迭代至不动点；
- idom 由支配集合导出：`idom(n)` 是 `n` 的严格支配者中支配集合最大者
  （同一节点的支配者按集合包含关系全序）。

## 验收场景（examples/demo.ts 输出）

```text
1) Diamond S -> {A, B} -> T
  idom(S) = null
  idom(A) = S
  idom(B) = S
  idom(T) = S

2) Insert bypass edge S -> T
  idom(T) = S

3) Remove bypass edge S -> T, then remove edge B -> T
  idom(T) = A

4) Counterexample: does A dominate T in the diamond?
  No. Witness path avoiding A: S -> B -> T
  Does S dominate T? Yes (no counterexample)
```

## 测试结果

```text
✔ diamond: idom(T) is the entry S
✔ incremental insert S->T (bypass edge): idom(T) stays S
✔ incremental deletes: after removing S->T and B->T, idom(T) jumps to A
✔ counterexample: path from S to T avoiding A in the diamond
✔ no counterexample when dominance holds
✔ counterexample after incremental insert: A still does not dominate T
✔ deletion can make nodes unreachable
✔ larger graph: chain through join node
ℹ pass 8 / fail 0
```
