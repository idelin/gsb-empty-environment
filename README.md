# DAG 可达性位图索引器（dag-reachability-index）

有向无环图（DAG）全节点可达性查询索引，TypeScript 实现，零第三方依赖。

## 核心设计

- **拓扑分层**：Kahn 拓扑排序 + 最长路径分层（`level[v] = max(level[p]) + 1`）。
- **位位置分配**：按 `(层级, 节点 id)` 排序分配位位置，使层级树状 DAG 中
  每个节点的下游可达位在比特串上聚集为连续段，最大化稀疏块压缩收益。
- **压缩位图**：`CompressedBitmap` 以 32 位块（chunk）稀疏存储，只为非零块
  分配内存；内存占用正比于非零块数，而非 O(n²/8) 稠密矩阵。
- **O(1) 查询**：`reach[u]` 的第 `pos[v]` 位即 u→v 可达性，一次位图位测试。
- **增量更新**：插入边 `u->v` 后，沿反向边 BFS，将差集
  `delta = (reach[v] ∪ {v}) - reach[x]` 以块级位运算（OR/AND-NOT）并行合并进
  u 的全部祖先，只传播新增位，无需重建闭包。
- **环检测**：插入前用位图（已构建）或 DFS（未构建）检查 `v ⇝ u`，成环即抛
  `CycleError`。

## 复杂度

| 操作 | 复杂度 |
| --- | --- |
| 构建 | O((V + E) · ⌈n/32⌉)，位并行 |
| 查询 `isReachable(u, v)` | O(1) |
| 插入边 | O(受影响祖先数 × 差集块数)，仅传播 delta |
| 内存 | O(非零 32 位块数)，远小于稠密矩阵 n²/8 |

## 运行

需要 Node.js ≥ 22.6（原生 TypeScript type stripping，无需编译、无依赖安装）：

```bash
npm test    # Node 内置测试运行器（node:test）
npm run demo
```

## API 摘要

```ts
import { DAGReachabilityIndex } from "./src/reachability-index.ts";

const idx = new DAGReachabilityIndex();
idx.addEdge("a", "b");          // 自动创建节点
idx.addEdge("b", "c");
idx.build();                    // 首次查询时也会自动构建
idx.isReachable("a", "c");      // true，O(1)
idx.addEdge("a", "c");          // 跨层边：增量位运算更新祖先可达集
idx.reachableFrom("a");         // ["a", "b", "c"]
idx.levelOf("c");               // 2
idx.stats();                    // 内存占用与压缩率
idx.nodeId("a");                // 批量查询可用 id 快路径
idx.isReachableById(0, 2);      // 绕开字符串哈希的 O(1) 查询
```

## 文件结构

- `src/compressed-bitmap.ts` — 稀疏压缩位图（32 位块、位并行 OR/差集/popcount）
- `src/reachability-index.ts` — 索引器：分层、闭包构建、增量更新、O(1) 查询
- `test/reachability.test.ts` — Node 内置测试（正确性对照 BFS、环检测、内存约束、性能）
- `examples/demo.ts` — 运行样例
