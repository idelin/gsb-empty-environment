# SDF Raymarcher

动态距离场（SDF）栅格化采样与等值面提取器 —— TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 功能

- **二维/三维有向距离场**：解析 SDF 图元（圆/矩形/球/长方体/圆环）与
  CSG 组合（并、平滑并、差），符号约定为内部为负、外部为正。
- **动态栅格化采样**：将任意解析 SDF 栅格化为 64×64（2D）或 64×64×64（3D）
  距离场网格；几何运动后调用 `rasterize` 即可动态更新。支持双线性/三线性
  插值采样与基于中心差分的梯度估计。
- **极速光线行进（Sphere Tracing）**：每一步按当前点的 SDF 值安全跨越
  最大空闲距离，通常 3–8 步收敛于碰撞表面。
- **等值面提取**：2D Marching Squares 输出零等值线段；3D Marching
  Tetrahedra 输出三角面片。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

## 运行

```sh
npm test    # 运行 node:test 测试（13 个用例）
npm run demo  # 运行样例：ASCII 渲染 64x64 距离场 + 光线行进 + 等值面提取
```

## 示例

```ts
import { circle, union, box2 } from "./src/geometry.ts";
import { SDFGrid2D } from "./src/grid.ts";
import { rayMarch2D } from "./src/raymarch.ts";

// 1. 由几何模型生成 64x64 有向距离场网格
const shape = union(circle([-0.5, 0], 0.7), box2([0.6, 0], [0.4, 0.4]));
const grid = new SDFGrid2D(64, 64, { min: [-2, -2], max: [2, 2] });
grid.rasterize(shape);

// 2. 从任意起点光线行进：每步跨越当前 SDF 值所界定的最大安全距离
const hit = rayMarch2D((p) => grid.sample(p), [-1.9, 1.6], [1, -0.75]);
console.log(hit.hit, hit.steps, hit.point); // true, 3, 碰撞点

// 3. 提取零等值面（轮廓线段）
const contour = grid.extractIsosurface();
```

## 项目结构

- `src/geometry.ts` — 解析 SDF 图元与 CSG 组合子
- `src/grid.ts` — `SDFGrid2D` / `SDFGrid3D`：栅格化、插值采样、梯度、等值面提取
- `src/raymarch.ts` — `rayMarch2D` / `rayMarch3D` 球体追踪光线行进
- `test/sdf.test.ts` — Node.js 内置测试（符号约定、64×64 栅格化、收敛步数、
  步进距离不变量、等值面精度、动态更新）
- `examples/demo.ts` — 可运行样例
