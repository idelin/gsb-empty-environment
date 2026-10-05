# threshold-clustering

可撤销的阈值聚类库（TypeScript）：按边权递增分批合并连通分量（Kruskal 式层次聚类），
支持在任意阈值下查询聚类、撤回末批并追加新批。

## 特性

- **分批合并**：边按权重递增逐批生效；同权边永远作为同一批原子生效，
  输入顺序不会产生不同的中间层（`fromEdges` / `appendEdges` 内部按权重排序分组）。
- **任意阈值查询**：`clustersAt(t)` 等价于在"权重 ≤ t 的边构成的阈值子图"上求连通分量。
- **可撤销**：`undoLastBatch()` 撤回末批后可继续追加新批（权重须 ≥ 新末批权重）。
- **成员与合并权重查询**：`componentOf`、`mergeWeight(u, v)`、`firstMergeWeight(v)`。
- 平行边、自环、孤点均为合法输入。

## 使用

```ts
import { ThresholdClustering } from "./src/threshold-clustering.ts";

const tc = ThresholdClustering.fromEdges(5, [
  [0, 1, 1],
  [1, 2, 2],
  [3, 4, 2], // 与 [1,2] 同权，同一批生效
]);

tc.clustersAt(1);      // [[0,1],[2],[3],[4]]
tc.clustersAt(2);      // [[0,1,2],[3,4]]
tc.mergeWeight(0, 2);  // 2
tc.componentOf(0, 2);  // [0,1,2]

tc.undoLastBatch();    // 撤回权重 2 的批
tc.appendBatch(3, [[2, 3]]);
tc.clustersAt(3);      // [[0,1,2,3,4]]
```

## API 摘要

| 方法 | 说明 |
| --- | --- |
| `new ThresholdClustering(n)` | n 个顶点（`0..n-1`），初始均为孤点 |
| `appendBatch(weight, edges)` | 追加一批同权边；与末批同权则并入末批，权重回退抛 `RangeError` |
| `appendEdges(weightedEdges)` | 追加带权边，按权重排序分组提交，与输入顺序无关 |
| `ThresholdClustering.fromEdges(n, edges)` | 从带权边一次性构建 |
| `undoLastBatch()` | 撤回末批，返回该批；无批可撤返回 `undefined` |
| `clustersAt(t)` | 阈值 t 下全部连通分量（含孤点） |
| `componentOf(v, t)` / `componentCountAt(t)` / `areConnected(u, v, t)` | 分量成员 / 数量 / 连通性 |
| `mergeWeight(u, v)` | u、v 进入同一分量的最早批次权重，不连通返回 `null` |
| `firstMergeWeight(v)` | v 最早脱离单点集的批次权重，孤点返回 `null` |

## 实现说明

并查集按 size 合并（树高 O(log n)），每批提交后保存一份快照；
查询按阈值二分定位快照，`mergeWeight` / `firstMergeWeight` 在批次序列上二分。
撤回即弹出末批快照并恢复上一状态，代价 O(n)。

## 测试

需要 Node ≥ 22.6（原生 TypeScript type stripping）：

```sh
npm test
```

测试用"阈值子图 + BFS"作为参考实现，对随机多重图（含平行边、自环、孤点、
同一批内多个分量）以及随机追加/撤回操作序列做全阈值对照。
