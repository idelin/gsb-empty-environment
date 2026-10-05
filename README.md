# annotation-coords 标注坐标库

对图像标注坐标做**裁剪、缩放、翻转、90° 旋转**的复合变换，支持从结果坐标**反查原图区域**，
并输出**被裁掉的部分**与**仍可恢复的部分**。

## 核心约定

- **像素边界坐标**：像素 `(i, j)` 覆盖 `[i, i+1) × [j, j+1)`，全程无 ±0.5 中心偏移。
- **矩形是半开区间** `[x0, x1) × [y0, y1)`：相邻瓦片共享边界但不重叠；宽或高为 0 即为空。
- **点与矩形语义不同**（`Vec2` vs `Rect`）：
  - 点是连续位置，翻转时 `x → W - x`；
  - 矩形是边界围成的区域，翻转/旋转时表现为边界互换：`[x0, x1) → [W - x1, W - x0)`。
- **整数取整只发生在最终栅格化**（`raster.ts`）：中间过程保留完整浮点精度，
  缩放后画布尺寸可以是分数（如 `101 → 50.5`）。

## 快速上手

```ts
import { TransformPipeline, makeRect, rasterizeRect } from "./src/index.ts";

const p = new TransformPipeline(800, 600)
  .crop(makeRect(50, 50, 750, 550))   // 裁剪（唯一会丢信息的操作）
  .rotate90(1)                        // 顺时针 90°，画布 500×700
  .scale(0.25)                        // 缩到 125×175
  .flipH();                           // 水平翻转

// 标注框跟随变换（正向），并可精确反查回原图（逆向）
const box = makeRect(100, 80, 120, 200);
const mapped = p.mapRect(box);
const back = p.unmapRect(mapped);     // === box

// 可恢复性分析
p.recoverableRegion();                // 当前画布内容在原图中的区域
p.clippedRegions();                   // 被裁掉的原图区域（0~4 个半开矩形）
p.steps[i].lostInOriginal;            // 第 i 步新裁掉的区域

// 逆序恢复：所有步骤的逆矩阵按相反顺序复合
p.restoreMatrix();

// 最后才取整：落到输出像素网格
rasterizeRect(mapped);
```

## API 一览

| 模块 | 内容 |
| --- | --- |
| `affine.ts` | 2×3 仿射矩阵：`compose` / `invert` / `translation` / `scaling` |
| `geometry.ts` | `Vec2`、`Rect`（半开）、`intersectRect`、`subtractRect`（差集 ≤4 块） |
| `pipeline.ts` | `TransformPipeline`：`crop` / `scale` / `flipH` / `flipV` / `rotate90`，正/逆向点与矩形映射，可恢复性分析 |
| `raster.ts` | `rasterizeRect`（floor/ceil）、`pixelAt`（floor）——唯一允许取整的地方 |

## 运行测试

需要 Node ≥ 23.6（内置 TypeScript 类型擦除，测试基于 `node:test`）：

```sh
node --test 'test/*.test.ts'   # 13 个测试
```

有 `tsc` 时可 `npm run typecheck` 做严格类型检查（代码只用可擦除语法，无 enum/namespace）。
