# Delaunay 三角剖分器（空间拓扑邻接图）

二维散点集 Delaunay 三角网构建器，纯 TypeScript 实现，零第三方依赖。

## 特性

- **空外接圆性质**：任意三角形的外接圆内部不包含第四个点
- **逐点动态插入**：`insert()` 增量插入，局部应用 Lawson 翻边算法恢复合法性
- **紧凑拓扑网格输出**：CCW 三角形列表、每个三角形 3 条对边的邻接邻居、CCW 凸包
- **鲁棒性**：超三角形自动扩围重建、边上插入 4 分裂、重复点去重、可见性行走 + 全扫描兜底定位

## 结构

- `src/geometry.ts` — 几何谓词：`orient2d` / `inCircle` / `circumcircle`
- `src/triangulator.ts` — 增量插入 + Lawson 翻边核心算法
- `src/mesh.ts` — 紧凑邻接网格构建（三角形、邻居、凸包）
- `examples/sample.ts` — 50 个随机测绘点运行样例
- `test/delaunay.test.ts` — Node.js 内置测试（`node:test`）

## 运行要求

Node.js ≥ 23.6（原生运行 TypeScript）；或 Node ≥ 22.6 并加
`--experimental-strip-types`。无需安装任何依赖。

## 使用

```bash
npm run sample   # 运行 50 点样例，输出三角网统计、凸包、邻接拓扑与空外接圆验证
npm test         # 运行 node:test 测试套件
```

## API

```ts
import { DelaunayTriangulator } from './src/index.ts';

const delaunay = new DelaunayTriangulator();
for (const p of points) delaunay.insert(p);   // 逐点动态插入
const mesh = delaunay.toMesh();
// mesh.triangles[i]   -> [a, b, c]      CCW 顶点索引
// mesh.neighbors[i]   -> [na, nb, nc]   各顶点对边的相邻三角形，-1 = 外边界
// mesh.hull           -> [h0, h1, ...]  CCW 凸包顶点索引
```
