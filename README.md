# graph-collapse-layout

图布局预处理库（TypeScript，零依赖）：把折叠子树映射到可见祖先节点，跨子树边聚合时保留原边
id，子树内部边（折叠后会成为自环）隐藏但记计数；展开时通过重算精确恢复原边，不做反向猜测。

## 语义

- 节点通过 `parent` 组成嵌套层级（森林）。节点 `n` **隐藏** 当且仅当其某个严格祖先被折叠。
- `representative(n)`：`n` 可见时为 `n` 本身，否则为最外层被折叠的祖先（嵌套折叠时映射到最外层）。
- 每条原边 `e = (u, v)` 逐条映射为 `(rep(u), rep(v))`：
  - 两端落到不同可见节点 → 跨子树边，按 `(source, target)` 聚合成一条 `AggregatedEdge`，
    `edgeIds` 按原边顺序保留全部原边 id（并行边因此不丢失）。
  - 两端落到同一可见节点且原边并非该节点上本真的自环 → 子树内部边，**隐藏**，但计数并
    记录原边 id 到该可见节点的 `hiddenEdgeCount` / `hiddenEdgeIds`。
  - 可见节点上本真的自环（`u === v === rep(u)`）保留为普通可见自环边。
- **展开 = 重算**：可见图是「原图 + 折叠集合」的纯函数，源边永不被修改，因此展开精确恢复
  原边，无需存储逆操作或反向推断。

## 用法

```ts
import { CollapsePreprocessor } from "graph-collapse-layout";

const pre = new CollapsePreprocessor({
  nodes: [
    { id: "A" },
    { id: "B", parent: "A" },
    { id: "X" },
  ],
  edges: [{ id: "e1", source: "B", target: "X" }],
});

pre.collapse("A");
pre.visibleGraph();
// nodes: A(collapsed, hiddenDescendants=1), X
// edges: [{ id: "A→X", edgeIds: ["e1"], count: 1 }]

pre.expand("A"); // 精确恢复 e1: B→X
```

`src/model.ts` 导出 `mapEdgesPerEdge`：独立编写的「原边逐条映射」参考模型，测试用它对
嵌套折叠、并行边、随机折叠/展开序列做逐状态交叉核对（`assert.deepEqual`），并校验
「每条原边 id 恰好出现在一条聚合边或一个节点的隐藏集合中」这一不变量。

## 测试

需要 Node ≥ 22.6（依赖原生 TypeScript 类型擦除与 `node:test`，无第三方依赖）：

```sh
node --test
```
