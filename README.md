# HMM Map Matching（时空轨迹地图匹配内核）

基于隐马尔可夫模型（HMM）的离散 GPS 轨迹路网匹配内核，TypeScript 实现，
零第三方依赖，Node.js ≥ 22.6 原生运行（类型擦除）。

## 模型

- **隐状态**：每个 GPS 观测在候选半径（默认 80 m）内各路段上的投影点。
- **发射概率（Emission）**：GPS 测量误差的高斯分布
  `p(z|r) ∝ exp(-d² / 2σ²)`，`σ` 默认 20 m（城市峡谷可取 15–30）。
- **转移概率（Transition）**：
  - 路网拓扑：Dijkstra 最短路计算候选点间的**路线距离**（有向图，
    同一边上禁止倒退，不连通即不可能）；
  - `p ∝ exp(-|routeDist − linearDist| / β)`，`β` 默认 10 m；
  - **速度合理性**：隐含速度超过 `maxSpeed`（默认 60 m/s）的转移被禁止。
- **解码**：Viterbi 算法在候选路段格图上求全局最可能路径。

## 运行

```bash
npm run example   # 城市峡谷多径漂移纠偏演示
npm test          # Node.js 内置测试（node:test）
```

## 样例输出（npm run example）

场景：沿 Main St 向东行驶至 x=400 后北转；第 3–6 个采样点因多径效应
向北偏移 35–55 m（已越过 50 m 外的平行路 North Ave）。

```
idx | raw drift (m) | snapped residual (m) | matched edge      | snap XY (m)
  0 |           6.0 |                  1.6 | cross-0-s         | (0.0, -1.6)
  1 |          13.0 |                 11.0 | main-0            | (39.0, 0.0)
  2 |           8.0 |                  3.2 | main-0            | (96.8, 0.0)
  3 |          39.3 |                 10.3 | main-1            | (160.3, 0.0)
  4 |          32.0 |                 21.9 | main-2            | (221.9, 0.0)
  5 |          45.3 |                  4.7 | main-2            | (245.3, 0.0)
  6 |          45.1 |                 12.3 | main-2            | (287.7, 0.0)
  7 |           7.1 |                  6.1 | main-3            | (343.9, 0.0)
  8 |           3.5 |                  2.0 | cross-400-n       | (400.0, 2.0)
  9 |           7.8 |                  7.3 | cross-400-n       | (400.0, 42.7)
mean |          20.7 |                  8.0 |
```

剧烈漂移点被全局路径一致性（拓扑 + 速度）约束拉回真实道路中心线，
平均定位误差从 20.7 m 降至 8.0 m。

## 测试结果（npm test）

```
✔ geometry: haversine computes known distance
✔ geometry: projection onto segment clamps and measures
✔ geometry: interpolateAlong walks a polyline
✔ network: Dijkstra shortest path and connectivity
✔ emission: Gaussian decreases with distance
✔ transition: rewards route/linear consistency, punishes detours
✔ transition: disconnected candidates are unreachable
✔ match: multipath drift in urban canyon snaps to true road
✔ match: snapped points lie on the road centerline
✔ match: rejects traces with no nearby road
ℹ pass 10 / fail 0
```

## 代码结构

- `src/geometry.ts` — haversine、点-线段投影、折线插值
- `src/network.ts` — 有向路网图 + Dijkstra 最短路
- `src/matcher.ts` — 候选生成、发射/转移概率、Viterbi 解码
- `examples/grid-city.ts` — 城市峡谷仿真路网与多径漂移轨迹生成
- `examples/urban-canyon.ts` — 演示程序
- `test/map-matcher.test.ts` — `node:test` 单元与端到端测试

## API

```ts
import { RoadNetwork } from "./src/network.ts";
import { MapMatcher } from "./src/matcher.ts";

const net = new RoadNetwork();
const a = net.addNode({ lon: 121.4, lat: 31.2 });
const b = net.addNode({ lon: 121.401, lat: 31.2 });
net.addEdge("main-0", a, b); // 有向边；双向道路请加反向边

const matcher = new MapMatcher(net, { sigma: 20, beta: 10 });
const results = matcher.match([
  { point: { lon: 121.4, lat: 31.2 }, timestamp: 1700000000000 },
  // ...
]);
// results[i]: { observation, candidate, edgeId, snapDistance }
```
