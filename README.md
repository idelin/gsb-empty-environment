# 网络瓶颈度与介数中心性近似器

图结构关键枢纽度评估器：计算所有节点对最短路径经过特定节点/边的频次（节点介数 + 边介数），
支持边权动态变动时刷新 Top-K 瓶颈节点，并在网关节点故障时给出路径分流建议与瓶颈转移报告。

纯 TypeScript 实现，零第三方依赖；利用 Node.js 原生类型擦除（type stripping）直接运行，
测试基于 Node 内置 `node:test`。

## 环境要求

- Node.js >= 22.6（`--experimental-strip-types`；23.6+ 可省略该标志）

## 运行

```bash
npm test    # Node 内置测试（13 个用例）
npm run demo  # 运行样例：混合拓扑 -> 动态边权 -> 网关故障 -> 瓶颈转移报告
```

## 结构

- `src/graph.ts` — 无向加权图，支持边权动态变动、节点/边增删、克隆
- `src/betweenness.ts` — 加权 Brandes 介数算法（节点 + 边）；精确模式遍历全部源节点，
  近似模式按随机种子抽样源节点并放大估计；附 Dijkstra 单源最短路
- `src/monitor.ts` — `BottleneckMonitor`：Top-K 瓶颈节点/边跟踪、`updateEdgeWeight` 动态刷新、
  `simulateNodeFailure`（`remove` 完全下线 / `degrade` 链路劣化）生成分流建议与瓶颈转移报告
- `src/topology.ts` — 星型、网状、星型+网状混合拓扑构造器
- `examples/demo.ts` — 运行样例
- `tests/` — `node:test` 测试

## 验收场景

混合网络（中心网关 GW + 网状骨干 A/B/C/D + 星型叶子 S1..S6，叶子带高权重备用链路）：

1. 稳态下 GW 介数 39，为首要瓶颈节点，GW-S* 为瓶颈边；
2. `updateEdgeWeight('GW', 'A', 4)` 后 Top-K 即时刷新，骨干节点进入榜单；
3. 模拟 GW 局部/完全故障：39 对节点流量需改道、0 对不可达，
   报告指出首要分流替代节点 A（承接 32.6% 改道路径），瓶颈榜由 GW 转移至骨干节点。
