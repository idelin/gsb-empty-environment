# sdf-toolkit

动态距离场（SDF）栅格化采样与等值面提取器 —— 纯 TypeScript 实现，零依赖，
直接由 Node.js（≥ 22.6，内置 TypeScript 类型擦除）运行，测试使用 Node 内置 `node:test`。

## 功能

- **二维/三维有向距离场（SDF）**：解析 SDF 图元（圆、矩形、线段、胶囊 / 球、盒、平面、圆环）
  与 CSG 组合（并 / 交 / 差 / 平滑并、平移 / 旋转 / 缩放），约定内部为负、外部为正。
- **栅格化采样器**：把解析 SDF 采样到规则网格（如 64×64 或 64³），
  提供双线性/三线性插值的连续场查询与中心差分梯度。
- **等值面提取**：2D Marching Squares（含鞍点消歧）输出线段；
  3D Marching Tetrahedra 输出焊接后的三角网格（法线一致朝外）。
- **极速光线行进（Sphere Tracing）**：每步前进 `|sdf(p)|` —— 当前点的最大安全空闲距离，
  几步之内几何级收敛到碰撞表面；既可行进解析场，也可行进采样网格（`stepScale` 安全系数）。

## 运行

```bash
# 需要 Node.js >= 22.6（推荐 23.x，类型擦除开箱即用）
node examples/demo.ts   # 端到端样例：64x64 栅格化 + ASCII 可视化 + 等值线/等值面 + 光线行进
node --test             # 运行全部测试（23 个用例）
```

## 代码结构

| 文件 | 说明 |
| --- | --- |
| `src/vec.ts` | 2D/3D 向量工具（元组表示） |
| `src/sdf.ts` | 解析 SDF 图元与 CSG 组合子 |
| `src/grid.ts` | `SdfGrid2D` / `SdfGrid3D` 栅格化采样、插值与梯度 |
| `src/contour2d.ts` | Marching Squares 等值线提取 |
| `src/surface3d.ts` | Marching Tetrahedra 等值面提取（顶点焊接、法线朝外） |
| `src/raymarch.ts` | Sphere Tracing 光线行进（2D/3D，解析场或采样场） |
| `examples/demo.ts` | 运行样例 |
| `test/*.test.ts` | `node:test` 测试 |

## 用法示例

```ts
import { circle, box2, smoothUnion2 } from './src/sdf.ts';
import { SdfGrid2D } from './src/grid.ts';
import { rayMarch2 } from './src/raymarch.ts';

const scene = smoothUnion2(0.15, circle([-0.25, 0], 0.45), box2([0.35, 0.1], [0.3, 0.25]));
const grid = SdfGrid2D.fromSdf(scene, { min: [-1, -1], max: [1, 1] }, 64, 64);

const hit = rayMarch2((p) => grid.sample(p), [-2, 0], [1, 0], { stepScale: 0.9 });
// => { hit: true, point: [-0.7, 0], distance: 1.3, steps: 9, ... }
```

## 备注

- 采样网格的三线性插值场沿体对角线并非线性，因此网格行进时建议
  `stepScale: 0.9` 作为安全系数；解析场可用默认的 `1.0`。
- `tsconfig.json` 供编辑器/CI 类型检查使用（`erasableSyntaxOnly`，与 Node 类型擦除兼容）。
