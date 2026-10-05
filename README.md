# annotation-geometry

标注坐标库：支持**复合**裁剪、缩放、翻转与 90 度旋转，并可从输出坐标**反查原图区域**。

## 核心约定

- **像素边界坐标**：点 `(x, y)` 是边界网格上的连续位置，不含面积；像素 `(i, j)` 覆盖 `[i, i+1) × [j, j+1)`。
- **矩形为半开区间** `[x0, x1) × [y0, y1)`：右/下边缘不属于矩形。点与矩形语义不同——
  翻转时点 `1` 映射到 `W-1`，而矩形 `[1,3)` 映射到 `[W-3, W-1)`；
  裁剪窗口包含判断对点按闭区间、对矩形按半开区间求交。
- **取整仅发生在最终栅格化**：所有变换计算保持 `float64`（允许分数裁剪窗口、分数缩放），
  只有 `rasterizePoint` / `rasterizeRect` 会做整数取整。

## 用法

```ts
import { TransformPipeline, rect, rasterizeRect } from "./src/index.ts";

const pipeline = TransformPipeline.forImage(640, 480)
  .crop(100.5, 50.25, 300, 200)   // 裁剪（唯一会丢弃数据的操作）
  .scale(1.5, 0.75)               // 缩放
  .flipX()                        // 水平翻转
  .rotate90CW();                  // 顺时针 90 度（另有 flipY / rotate90CCW）

// 反查原图区域（输出坐标 -> 原图坐标）
const srcRegion = pipeline.mapRectBackward(rect(10, 20, 100, 120));

// 正向映射（原图坐标 -> 输出坐标）；被裁掉的点/矩形返回 null
const out = pipeline.mapRectForward(rect(120, 60, 200, 100));

// 被裁掉部分 vs 仍可恢复部分（均在原图坐标系）
const { recoverable, clipped } = pipeline.analyze();

// 逆序恢复：invert() 生成逆序逆变换管线；verifyRoundTrip() 验证 B∘F = F∘B = I
const inverse = pipeline.invert();
console.log(pipeline.verifyRoundTrip()); // { ok: true, maxError: 0 }

// 最终栅格化（唯一的取整入口）
const pixels = rasterizeRect(srcRegion!, "cover"); // floor 左上 / ceil 右下
```

## 结构

- `src/geometry.ts` — 点 / 半开矩形、求交、相减（被裁区域拆分为至多 4 个矩形）
- `src/matrix.ts` — 2D 仿射矩阵（复合、求逆、轴对齐矩形映射）
- `src/pipeline.ts` — 复合变换管线：步骤记录、正/反向映射、裁剪分析、逆序恢复验证
- `src/rasterize.ts` — 栅格化取整（`cover` / `nearest` 两种模式）

## 运行

需要 Node ≥ 22.6（利用原生 TypeScript 类型擦除，零依赖）：

```sh
node --test test/*.test.ts   # 16 个测试
node examples/demo.ts        # 示例
```
