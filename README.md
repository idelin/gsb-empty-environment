# Network Bottleneck Analyzer

网络瓶颈度与介数中心性近似器：基于 Brandes 算法计算加权无向图中所有节点对最短路径
经过特定节点/边的频次（介数中心性），支持边权动态变更后的 Top-K 瓶颈节点刷新、
节点故障模拟与路径分流建议。

## 环境要求

- Node.js >= 23.6（原生运行 TypeScript，零依赖）
- Node.js 22.6+ 亦可，需加 `--experimental-strip-types` 标志
- 如需完整类型检查：`npm i -D typescript @types/node && npx tsc --noEmit`

## 运行

```bash
node --test        # 运行内置测试（node:test）
node src/demo.ts   # 运行演示：星型+网状混合拓扑、网关故障模拟
```

## 模块

- `src/graph.ts` — 加权无向图，支持动态边权更新、节点摘除与恢复
- `src/betweenness.ts` — Brandes 精确介数（节点+边）、随机源点抽样近似（种子可复现）、Dijkstra 最短路径
- `src/monitor.ts` — `BottleneckMonitor`：Top-K 瓶颈跟踪、`updateEdgeWeight` 动态刷新、
  `simulateNodeFailure` 故障模拟（瓶颈转移报告）、`suggestReroutes` 分流建议

## API 示例

```ts
import { WeightedGraph } from "./src/graph.ts";
import { BottleneckMonitor } from "./src/monitor.ts";

const g = new WeightedGraph();
g.addEdge("L1", "GW", 1);
g.addEdge("GW", "R1", 1);

const monitor = new BottleneckMonitor(g, { k: 5 });
monitor.topK();                       // 当前 Top-K 瓶颈节点
monitor.updateEdgeWeight("GW", "R1", 8); // 边权变动，Top-K 自动刷新
const report = monitor.simulateNodeFailure("GW");
report.reroute.primaryAlternative;    // 首要分流替代节点
report.newTopK;                       // 故障后的新瓶颈排名
```

大图可启用近似模式：`new BottleneckMonitor(g, { k: 10, approximate: true, samples: 32, seed: 1 })`。

## 运行样例（`node src/demo.ts`）

演示网络：星型叶子 L1-L6 汇聚到中心网关 GW；网状核心 R1-R4 全互联；
备用聚合器 AGG 以更高代价连接叶子与核心。

```
=== Hybrid star+mesh network: 12 nodes (GW, AGG, R1-R4, L1-L6) ===
Initial top-5 bottleneck nodes:
   1. GW   score=39.00
   2. R2   score=1.50
   3. R3   score=1.50
Top-3 bottleneck edges:
  GW-L3 score=10.00

=== Dynamic edge-weight update: GW-R1 congestion (1 -> 8) ===
Top-5 after weight update:
   1. GW   score=39.00
   2. R2   score=3.83
   3. R3   score=3.83
   4. R4   score=2.33

=== Bottleneck shift report: failure of GW ===
Top-K after failure:
   1. AGG  score=39.00
   2. R2   score=7.00
   3. R3   score=7.00
Biggest gainers (betweenness delta):
  AGG  +39.00
Affected node pairs: 39, disconnected: 0
Primary reroute alternative: AGG
Alternative ranking:
  AGG  redirected= 39 share=76.5%
  R2   redirected=  6 share=11.8%
  R3   redirected=  6 share=11.8%
Sample reroutes:
  R1 -> L1: [R1>GW>L1]  =>  [R1>R2>AGG>L1]
```

## 测试结果（`node --test`，13/13 通过）

```
✔ star topology: center carries all inter-leaf traffic
✔ path graph: middle node has highest betweenness
✔ dumbbell: bridge edge and bridge endpoints dominate
✔ weights redirect traffic: heavier path lowers betweenness
✔ approximation with all nodes as samples equals exact result
✔ approximation preserves top-1 hub on star+mesh hybrid
✔ shortestPath honors weights and returns null when disconnected
✔ hybrid network: central gateway GW is the top bottleneck
✔ dynamic edge-weight update refreshes top-K ranking
✔ gateway failure: report names AGG as primary reroute alternative
✔ reroute suggestions avoid the failed node and stay connected
✔ graph and scores are fully restored after failure simulation
✔ leaf failure has no reroute alternative for its own traffic
ℹ tests 13  ℹ pass 13  ℹ fail 0
```
