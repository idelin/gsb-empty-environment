# threshold-clustering

可撤销的阈值聚类库（TypeScript）：按边权递增分批合并连通分量（单链接聚类），
支持在任意阈值下查询聚类结果，并可撤销最后一批、追加新批。

## 核心语义

- **同权边原子生效**：同一权重的所有边作为一批（weight level）同时生效，
  批内边先排序再合并，输入顺序不会产生不同的中间层或不同的内部状态。
- **可撤销**：`undoLastBatch()` 移除最后一批并精确恢复并查集状态；
  之后可追加新批（权重不得小于当前末批；等于末批时并入该批）。
- **任意阈值查询**：每层保存快照，`componentsAt(t)` / `membersAt(v, t)` /
  `connectedAt(u, v, t)` 直接读取 `weight <= t` 的最后一层快照。
- **最早合并权重**：`mergeWeight(u, v)` 在层序列上二分（连通性单调），
  返回 u、v 最早进入同一分量的权重；`u === v` 返回 `-Infinity`，
  永不连通返回 `Infinity`。

## API

```ts
import { ThresholdClustering } from "threshold-clustering";

const c = new ThresholdClustering(6);          // 顶点 0..5（含孤点）
c.appendBatch([{ u: 0, v: 1, w: 2 }, { u: 3, v: 4, w: 2 }]); // 同权一批
c.appendBatch([{ u: 1, v: 2, w: 5 }]);

c.componentsAt(2);   // [[0,1],[2],[3,4],[5]]
c.membersAt(0, 5);   // [0, 1, 2]
c.mergeWeight(0, 2); // 5
c.undoLastBatch();   // 撤销权重 5 那一批
c.appendBatch([{ u: 2, v: 3, w: 4 }]); // 分叉追加新批
```

平行边（重复边）天然幂等；孤点始终作为单点分量出现在结果中。

## 运行测试

需要 Node >= 22.6（依赖原生类型擦除运行 TS）：

```sh
node --test test/*.test.ts
```

测试以「阈值子图 BFS」为参照实现做差分核对，覆盖：平行边、孤点、
多个分量同批合并、撤销/再追加的历史分叉、乱序输入确定性，
以及随机追加/撤销序列下全阈值对照。
