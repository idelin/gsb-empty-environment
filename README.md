# graph-collapse-view

TypeScript 图布局预处理库：把折叠子树映射到可见祖先，跨子树边聚合时保留原边
id，子树内部边隐藏但记录计数；展开时从原边重新推导视图，精确恢复，不做反向
猜测。

## 模型

- `Graph`：原始图，节点构成森林（每个节点至多一个父节点），边有稳定 id。
  折叠/展开从不修改原图。
- `CollapseView`：只保存一个 `collapsed: Set<string>`。视图（`getView()`）永远
  从原边逐条推导：
  1. 每条原边的两个端点分别映射到各自的 `visibleAncestorOf`（自身可见则为自身，
     否则为最近的可见祖先）；
  2. 两端落在同一个折叠节点上 → 该边是子树内部边：隐藏，并在该折叠节点上
     累计 `hiddenEdgeCount`；
  3. 否则边保持可见，按有向 `(source, target)` 聚合成 `AggregatedEdge`，
     `edgeIds` 按原图插入顺序保留全部原边 id（并行边自然归入同一聚合）。

展开只是从 `collapsed` 集合中删除节点后重新推导，因此原边精确恢复——包括
嵌套折叠（先折外层再折内层、任意顺序展开）和并行边。

## 用法

```ts
import { CollapseView, Graph } from "graph-collapse-view";

const graph = new Graph();
graph.addNode({ id: "r" });
graph.addNode({ id: "a", parentId: "r" });
graph.addNode({ id: "a1", parentId: "a" });
graph.addNode({ id: "b", parentId: "r" });
graph.addEdge({ id: "e1", source: "a1", target: "b" });

const view = new CollapseView(graph);
view.collapse("a");
view.getView();
// nodes: r, a(collapsed, hiddenNodeCount=1), b
// edges: [{ source: "a", target: "b", edgeIds: ["e1"] }]
view.expand("a"); // 原边 e1 精确恢复
```

## 测试

需要 Node >= 22.6（TypeScript 类型擦除运行）：

```sh
npm test
```

测试包含一个独立编写的"原边逐条映射"参考模型
（`test/collapse.test.ts` 中的 `referenceView`），通过确定性随机操作序列
（500 步 collapse/expand）与库实现逐步比对，覆盖嵌套折叠、并行边与取消折叠。

## 环境说明

源码只使用可擦除 TypeScript 语法（`erasableSyntaxOnly`），可直接被
Node 22.6+ 执行；也可用 `tsc` 编译后接入任意构建管线。
