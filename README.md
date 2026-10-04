# dynamic-transitive-closure

TypeScript 实现的**动态传递闭包**库：支持有向边的批量增加与删除，增量维护全部可达对，
输出可达关系**差量**及每对可达关系的一条**见证路径**，并内置与全量搜索对照的 `verify()`。

## 特性

- **批量增删**：`addEdges` / `removeEdges` / `update({ add, remove })`。
- **正确的删除语义**：删除一条边时，只修复见证路径确实经过该边的可达对；
  修复方式是在当前图上重新搜索替代路径——**由其他路径支撑的可达关系绝不会被误删**。
- **差量输出**：每次更新返回 `{ added, removed }`，`added` 带见证路径，`removed` 带删除前的旧路径。
- **见证路径**：`witnessPath(u, v)` 返回一条具体节点序列；库保证其每条边都存在于当前图中。
- **环与重复边**：按简单有向图处理，重复边幂等去重；支持自环与环（环上节点可达自身）。
- **对照验证**：`recompute()` 从每个节点 BFS 全量重算；`verify()` 报告增量结果与全量结果的差异。

## 快速开始

```ts
import { DynamicTransitiveClosure } from './src/index.ts';

const tc = new DynamicTransitiveClosure<string>();
tc.addEdges([['a', 'b'], ['b', 'c'], ['a', 'c']]);

console.log(tc.witnessPath('a', 'c')); // [ 'a', 'c' ]

// 删除直达边：(a,c) 由 a->b->c 支撑，必须保留
const delta = tc.removeEdges([['a', 'c']]);
console.log(delta.removed);            // []
console.log(tc.witnessPath('a', 'c')); // [ 'a', 'b', 'c' ]

// 再删 b->c，(a,c) 才真的消失
const d2 = tc.removeEdges([['b', 'c']]);
console.log(d2.removed); // [ { from: 'a', to: 'c', ... }, { from: 'b', to: 'c', ... } ]

console.log(tc.verify().ok); // true —— 与全量搜索一致
```

## API 概览

| 方法 | 说明 |
| --- | --- |
| `addEdges(edges)` | 批量加边（重复边为空操作），返回 `Delta` |
| `removeEdges(edges)` | 批量删边（不存在的边为空操作），返回 `Delta` |
| `update({ add, remove })` | 同一批内先删后加，返回合并 `Delta` |
| `isReachable(u, v)` / `witnessPath(u, v)` | 查询可达性 / 见证路径 |
| `reachableFrom(u)` / `entries()` / `edges()` | 枚举查询 |
| `recompute()` | 全量 BFS 重算（对照基准） |
| `verify()` | 与全量重算对照，返回 `{ ok, missing, extra }` |

## 算法与复杂度

- **加边** `(a,b)`：新可达对 = （能到 `a` 的点 ∪ {a}） × （`b` 能到的点 ∪ {b})，
  取加边前快照做笛卡尔积，一趟完成，环不遗漏。代价 `O(|V|²)` 上界。
- **删边**：通过「边 → 使用该边作见证的可达对」反向索引，仅修复受影响的可达对；
  每对在当前图上 BFS 重搜替代路径（`O(|V|+|E|)`），找不到才从闭包中移除。
  因此删除开销与受影响规模成正比，而非全量重算。

## 运行测试

需要 Node ≥ 22.6（使用内置类型剥离运行 TS，无需编译）：

```sh
node --experimental-strip-types --test
```

或使用任意 TypeScript 工具链（`tsc` / `vitest` 等）按常规方式构建与测试。
