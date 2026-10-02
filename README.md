# dynamic-convex-hull-evolver

带时效性的空间点集凸包演化器（TypeScript，零依赖）。

每个坐标点携带进入时间与 TTL；时钟推进时新点加入、老点自然过期，
演化器增量维护当前存活点集的二维凸包顶点序列（逆时针），
避免每个时隙全量重跑 Graham 扫描。

## 运行环境

- Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；推荐 >= 23.6）

## 使用

```ts
import { DynamicConvexHullEvolver } from "./src/index.ts";

const evolver = new DynamicConvexHullEvolver();
evolver.advanceTo(0);                       // 时钟推进到时隙 0，过期点在此刻淡出
evolver.addPoint({ id: "uav-1", x: 10, y: 5 }, 4); // 进入系统，TTL=4 个时隙
evolver.advanceTo(3);
const hull = evolver.getHull();             // 当前存活点集的凸包顶点（CCW）
```

## 复杂度

- 插入：O(h) 切线链接入（h 为凸包顶点数）；落在包内则零代价跳过
- 内部点过期：O(log n) 堆弹出，凸包不动
- 凸包顶点过期：懒标记后触发一次 O(n log n) Graham 重建（摊还罕见）

## 命令

```sh
npm test    # Node 内置测试运行器（node:test）
npm run demo  # 无人机编队散点演化样例
```

## 样例输出（`npm run demo`）

```
slot | live | hull vertices (CCW)                         | area
------------------------------------------------------------------------------
   0 |    2 | (9.5,23.6) (24.8,15.4)                        | 0.0
   1 |    5 | (22.2,14.2) (24.8,15.4) (24.3,23) (9.5,23.6) (17.2,12.6) | 109.6
   ...
  11 |   12 | (33,12.3) (37.8,7.7) (38.1,7.6) (47.2,6) (58.1,9) (61.1,15.7) (59.1,17.9) | 196.7
------------------------------------------------------------------------------
stats: 22 incremental inserts, 11 interior inserts (no hull change), 24 TTL expirations, only 10 full rebuilds
```

## 测试结果（`npm test`）

```
✔ grahamScan: square with interior and collinear edge points
✔ grahamScan: degenerate sets
✔ evolver: incremental inserts always match a full Graham scan
✔ evolver: TTL expiry fades points out of the live set
✔ evolver: interior expiry never triggers a full rebuild
✔ evolver: hull-vertex expiry triggers exactly one lazy rebuild
✔ evolver: re-adding an id replaces the stale observation
✔ evolver: rejects non-positive ttl and backwards time travel
✔ evolver: fuzz against brute-force oracle over a random timeline
✔ evolver: collinear-heavy grid stays strictly convex
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

## 结构

- `src/geometry.ts` — 叉积、Graham 扫描、多边形面积
- `src/evolver.ts` — `DynamicConvexHullEvolver`：TTL 最小堆 + 增量凸包
- `test/evolver.test.ts` — node:test 单元测试与随机模糊对照
- `examples/drone-swarm.ts` — 无人机编队安全空域边界演化样例
