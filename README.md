# soft-coverage-raster

纯 TypeScript 实现的软边覆盖率光栅化与遮罩合成，不依赖 Canvas 滤镜或任何图像处理库
（零运行时依赖，Node.js 内置 `node:test` 直接运行 `.ts`）。

## 功能

- **矩形与路径**：`PathBuilder` 支持 `moveTo/lineTo/quadraticTo/cubicTo/closePath/rect`，
  贝塞尔曲线在设备空间按平坦度自适应细分为折线。
- **软边覆盖率**：`fillPath` 对每条轮廓按像素做 Sutherland–Hodgman 裁剪并累加有向面积，
  得到逐像素精确覆盖率，结果钳制在 `[0,1]`（边缘覆盖不越界，重叠同向轮廓 winding≥2 时按
  nonzero 规则钳到 1）。
- **布尔并交差**（覆盖率代数，结果均在 `[0,1]`）：
  - 并 `union(a,b) = a + b − a·b`
  - 交 `intersect(a,b) = a·b`
  - 差 `difference(a,b) = a·(1−b)`
- **反向遮罩**：`invertMask(mask, region)` 只在指定画布范围内取补（`1−m`），范围外恒为 0；
  同一范围内两次取补还原原遮罩。
- **多重遮罩**：`applyMask(base, m1, m2, …) = base·m1·m2·…`，重复应用按乘法定义
  （`applyMask(base, m, m) = base·m²`）。
- **嵌套坐标变换**：`TransformStack`（save/restore/translate/rotate/scale）等价于矩阵左乘，
  形状在用户空间定义、变换到设备空间后再光栅化。
- **逐像素参考比对**：`referenceMask` 用独立的 32×32 超采样 + 非零环绕数判定作为参考，
  与精确覆盖率逐像素比较（容差 0.08）。

## 运行

需要 Node.js ≥ 23.6（原生 TypeScript type-stripping）：

```sh
npm test        # node --test，18 个测试
npm run sample  # 运行样例场景，输出比对报告、ASCII 预览并写出 sample/output.ppm
```

## 样例输出（实测）

48×36 画布：嵌套变换下的贝塞尔 blob 与五角星，做并/交/差、区域内反向遮罩、
链式遮罩（union × inverse × A），与 32×32 超采样参考逐像素比对：

```
PASS  blob mask (nested transform)       maxErr=0.0062  range=[0.000, 1.000]
PASS  star mask (nested transform)       maxErr=0.0019  range=[0.000, 1.000]
PASS  union                              maxErr=0.0062  range=[0.000, 1.000]
PASS  intersect                          maxErr=0.0034  range=[0.000, 1.000]
PASS  difference                         maxErr=0.0062  range=[0.000, 1.000]
PASS  inverse mask (region only)         maxErr=0.0019  range=[0.000, 1.000]
PASS  chained masks (union * inverse * A) maxErr=0.0107  range=[0.000, 1.000]
PASS  composited RGBA image vs reference   maxChannelDiff=2
ALL CHECKS PASSED
```

## 测试结果（实测）

```
ℹ tests 18
ℹ pass 18
ℹ fail 0
```

覆盖：分数坐标矩形精确覆盖率、`[0,1]` 边界、旋转矩形/贝塞尔/二次曲线/带洞路径对参考的
逐像素比对、布尔代数与硬遮罩集合语义、区域限制反向遮罩与双重取补、De Morgan 律、
重复遮罩乘法定义、source-over 合成数值、端到端场景对参考图像的逐通道比对。

## 已知限制

- 覆盖率按轮廓有向面积累加后钳制到 `[0,1]`，与 nonzero 规则在 winding∈{0,1} 时完全一致；
  winding≥2 与空洞混合的退化情形按钳制处理。
- 反向遮罩区域为整数矩形（自动钳到画布内）。
