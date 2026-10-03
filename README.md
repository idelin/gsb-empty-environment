# incremental-scc

动态有向图的**增量强连通分量(SCC)维护器**(TypeScript, 零依赖)。

## 特性

- 顶点动态增删、有向边插入/删除
- 增量缩点: 始终维护图的分量缩点(condensation), 无需全量重算
- 局部遍历:
  - **边插入** — 在缩点上做局部前向/后向 BFS, 仅当 `v` 可达 `u` 时, 合并位于 `v~>u` 路径上的分量
  - **边删除** — 仅在受影响的原分量诱导子图内做局部 Tarjan, 按需分裂, 不触碰无关分量
- 每次变更返回 `SCCDiff` 差量(合并 / 分裂 / 移除记录)
- 分量标识 = 成员中的**最小节点 ID**, 在增量变更中保持稳定

## 环境要求

Node.js >= 23.6(原生运行 TypeScript)。Node 22.6+ 可加 `--experimental-strip-types` 运行。

## 使用

```ts
import { DynamicSCC } from "./src/incremental-scc.ts";

const g = new DynamicSCC();
g.addEdge("A", "B");
g.addEdge("B", "C");
const diff = g.addEdge("C", "A");   // 三角形环闭合
// diff.merged[0].component => { id: "A", members: ["A", "B", "C"] }

g.componentOf("B");                 // "A"
g.removeEdge("B", "C");             // 分裂为 {A} {B} {C}
```

## API

| 方法 | 说明 |
| --- | --- |
| `addVertex(v)` / `removeVertex(v)` | 顶点增删(删顶点级联删关联边) |
| `addEdge(u, v)` / `removeEdge(u, v)` | 边增删, 返回 `SCCDiff` |
| `componentOf(v)` | 顶点所属分量 ID |
| `getComponents()` | 全部分量快照 |
| `getLastDiff()` | 最近一次操作的差量 |

`SCCDiff` 结构: `{ merged: MergeRecord[], split: SplitRecord[], removed: ComponentInfo[] }`。

## 运行

```bash
npm test        # Node.js 内置测试 runner
npm run demo    # 验收场景演示
```

演示输出(三角形环合并 -> 加 D -> 删边精准分裂, D 不受影响):

```
=== 插入 C->A (三角形环闭合) ===
  合并: [A, B, C] -> 分量 A { A, B, C }
=== 插入 C->D ===
  (无分量变化)
=== 删除 B->C (环断开) ===
  分裂: 分量 A { A, B, C } -> A{ A }, B{ B }, C{ C }
  当前分量: A{ A }  B{ B }  C{ C }  D{ D }
```
