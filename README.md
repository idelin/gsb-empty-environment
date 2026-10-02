# 双向增量拓扑排序与环检测器

大型依赖图的动态拓扑序维护引擎（TypeScript，零依赖）。基于 Pearce–Kelly 双向局部搜索算法：

- **前向边快路径**：`ord(u) < ord(v)` 时 O(1) 直接插入，不可能成环；
- **后向边局部重染色**：仅在序号窗口 `[ord(v), ord(u)]` 内做前向 DFS（从 v）+ 反向 DFS（从 u），
  复用受影响节点原有序号槽位重新分配（δB 前移、δF 后移），窗口外节点完全不动；
- **环检测**：前向 DFS 一旦从 v 可达 u，立即拒绝并通过 parent 链重建完整因果环路
  `[v, ..., u, v]`，图与拓扑序保持原状；
- **批量原子提交**：`tryAddEdges` 全有或全无，任一边成环则整体回滚，适合并发到达的依赖变更。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤）。

## 运行

```bash
npm test    # Node.js 内置测试（node:test）
npm run demo  # 运行样例
```

## API

```ts
import { IncrementalTopoSort } from './src/incremental-topo.ts';

const g = new IncrementalTopoSort<number>();
g.addEdge(1, 2);                       // { ok: true, affected: [] }
g.addEdge(4, 2);                       // 成环 -> { ok: false, cycle: [2, 3, 4, 2] }
g.tryAddEdges([[2, 4], [4, 5]]);       // 批量原子提交
g.order();                             // 当前拓扑序
g.rank(v);                             // 节点拓扑序号
g.validate();                          // 校验拓扑序一致性
```

## 验收样例输出

```
=== 初始依赖链 1->2->3->4 ===
拓扑序: [ 1, 2, 3, 4 ]

=== 场景一：增加边 4->2（成环，应立即拒绝） ===
拒绝！检测到因果死锁环路: 2 -> 3 -> 4 -> 2
拓扑序（应保持不变）: [ 1, 2, 3, 4 ]

=== 场景二：增加边 1->4（前向边，快速接受） ===
接受。受影响重排节点: 无（快路径）
拓扑序（保持单调）: [ 1, 2, 3, 4 ]
```

## 复杂度

| 操作 | 复杂度 |
| --- | --- |
| 前向边插入 | O(1) |
| 后向边插入 / 环检测 | O(‖δF‖ + ‖δB‖)，仅触及受影响子图 |
| 批量原子提交 | O(V + E) 快照 + 逐边增量 |

## 文件结构

- `src/incremental-topo.ts` — 引擎实现
- `test/incremental-topo.test.ts` — 12 个测试（含验收边界、随机 DAG 压力测试）
- `examples/demo.ts` — 运行样例
