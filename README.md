# 双向增量拓扑排序与环检测器

大型依赖图的动态拓扑序维护引擎，基于 Pearce & Kelly 增量拓扑排序算法，纯 TypeScript 实现，零依赖（Node.js >= 22.6 原生运行 TypeScript）。

## 特性

- **并发加边**：`addEdge` 返回 Promise，内部通过 Promise 链串行化，支持并发调用
- **即时环检测**：加边成环时立即拒绝，抛出 `CycleError`，`cycle` 字段为完整因果死锁环路（如 `[2, 3, 4, 2]`），且图与拓扑序完全不变
- **双向局部重排**：序号违逆时，在受影响区间 `[lb, ub]` 内做前向 DFS（从 y）+ 反向 DFS（从 x）双向搜索，仅重新染色受影响节点的拓扑序号，区间外节点序号不变
- **拓扑单调性**：任意时刻保证所有边 `x->y` 满足 `label[x] < label[y]`

## API

```ts
const g = new IncrementalTopo();
await g.addEdge(1, 2);        // 增加依赖边 1 -> 2
g.getOrder();                 // 当前拓扑序节点列表
g.indexOf(1);                 // 节点拓扑序号
g.isValid();                  // 校验拓扑单调性
```

## 运行

```sh
npm test      # Node.js 内置测试运行器
npm run demo  # 运行样例
```

## 验收场景

初始依赖 `1->2->3->4`：

1. 增加 `4->2` → 抛出 `CycleError`，`cycle = [2, 3, 4, 2]`，拓扑序保持 `[1, 2, 3, 4]`
2. 增加 `1->4` → 快速接受（序号已满足），保持拓扑单调性
