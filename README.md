# Delaunay Topology

二维散点集的 Delaunay 三角网构建器（TypeScript，零依赖）。
逐点动态插入 + 局部 Lawson 翻边恢复空外接圆性质，输出带邻接关系的紧凑三角拓扑网格与外边界凸包。

## 环境要求

- Node.js ≥ 22.18（或 ≥ 23.6）：直接以 type-stripping 方式运行 `.ts`，无需编译、无需安装依赖。
- 本仓库开发时使用 Node v23.9.0 验证。

## 运行

```bash
npm run demo    # 50 个随机测绘点：构建、验证、输出邻接图与凸包
npm test        # Node.js 内置测试运行器（node:test）
```

## 结构

- `src/geometry.ts` — 几何谓词：`orient2d`（定向）、`inCircle`（外接圆行列式）及其相对误差界 `inCircleBound`、`circumcenter`。
- `src/delaunay.ts` — `Delaunay` 类：超三角形初始化、行走法点定位、1→3 / 2→4 分裂、Lawson 翻边（显式栈）、`mesh()` 输出。
- `test/delaunay.test.ts` — `node:test` 验收测试。
- `examples/demo.ts` — 运行样例。

## 算法要点

- **增量插入**：所有点包含在一个随点集自适应缩放的超三角形内；插入点越界时按当前点集重建超三角形并重放插入。
- **点定位**：从上次的三角形出发沿边行走（walk），失败时退化为线性扫描，保证鲁棒。
- **Lawson 翻边**：每次插入后把新点对面的边压入栈；弹出的边若满足「四边形严格凸 且 对角点在新点外接圆内」则翻边，并把新产生的两条对边继续压栈。
- **数值判定**：`inCircle` 行列式的显著性阈值取 `1e-12 * inCircleBound`（与行列式自身量级相对），避免绝对阈值在大坐标/瘦三角形下失效；近共圆情形不翻边，防止抖动。
- **输出**：`mesh()` 过滤掉与超三角形顶点相连的三角形，重编号后给出每个三角形的 `vertices`（逆时针）、`neighbors`（对边邻居，-1 表示边界）、外接圆圆心/半径，以及由边界有向边串联出的 CCW 凸包顶点序列。

## API

```ts
import { Delaunay } from "./src/index.ts";

const delaunay = new Delaunay();
delaunay.insert({ x: 12.3, y: 45.6 });   // 逐点动态插入
const mesh = delaunay.mesh();
// mesh.points      点集
// mesh.triangles   [{ vertices, neighbors, circumcenter, circumradius }]
// mesh.hull        凸包顶点索引（CCW）
```

## 验收样例输出（50 随机点）

```
points:              50
triangles:           88  (Euler check 2n-2-h = 88)
convex hull vertices: 10
hull (CCW indices):  [18, 45, 34, 11, 33, 39, 43, 36, 24, 5]
empty-circumcircle violations: 0 (worst relative depth: 0.00e+0)
minimum interior angle:        2.82 deg (Delaunay maximizes it)
boundary edges: 10 (== hull vertex count: true)
```

## 测试结果

```
ℹ tests 9
ℹ pass 9
ℹ fail 0
```

覆盖：50 随机点空外接圆性质、Euler 公式 T=2n-2-h、邻接对称性与共享边一致性、
凸包凸性/CCW/包含全部点、逐点增量插入（含越界点触发重建）、正方形+中心已知情形、
重复点去重、少于 3 点退化输入、全部三角形逆时针非退化。

另经压力验证：100 组种子 × 50 点全部满足空外接圆；2000 点增量插入
（3973 三角形 / 凸包 25 顶点）零违规且满足 Euler 公式，耗时约 1 秒。
