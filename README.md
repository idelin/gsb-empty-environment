# Raster Transform Pipeline

纯 TypeScript 栅格变换管线：裁剪、任意仿射、面积缩小、双线性放大。零依赖，
不调用 Canvas 滤镜或任何现成图像处理库；所有重采样在浮点线性预乘颜色空间中
手工实现，用 Node.js 内置测试运行器（node:test）验证。

要求 Node >= 23.6（原生 TypeScript 类型擦除 + node:test）。

    npm test           # 33 个测试
    npm run example    # 运行 examples/demo.ts，输出见 examples/sample-output.txt

## 贯穿全管线的规则

**像素中心坐标**：像素 (i,j) 覆盖连续区域 [i,i+1) x [j,j+1)，中心在
(i+0.5, j+0.5)。每个 stage 持有正向仿射 forward（源→目标），采样统一使用
其逆 inverse（目标→源）。构造时即求逆，奇异矩阵（|det| <= 1e-12 * scale^2）
直接抛错拒绝。

**线性预乘颜色**：sRGB 字节在管线入口经精确 EOTF 转线性并预乘 alpha，全部
滤波在 Float32Array 线性预乘空间进行，出口才反预乘并编码回 sRGB。因此
“透明红 + 不透明蓝”缩小得到 (0,0,255,~128)，绝无红晕；黑白各半混合得到
188（线性 0.5），而非 sRGB 空间错误平均出的 128。

**边界规则**：源图界外一律视为透明黑 (0,0,0,0)。copy/bilinear 的界外采样
贡献 0；area 的归一化分母始终是完整足迹多边形面积（含界外部分），因此贴边
像素按定义变透明，而非被重新归一化。

## 滤波语义

- copy：仅允许整数平移（构造时校验），逐像素复制，界外写透明黑。
- bilinear：在 inverse(目标中心) 处取 2x2 线性插值，越界邻居按透明黑计。
- area：目标像素方格经 inverse 映射为源空间平行四边形，与每个源像素方格
  做精确凸多边形裁剪（Sutherland-Hodgman）求面积权重，加权和除以足迹总面积。
  旋转后缩小由同一积分器处理，覆盖面积守恒（测试断言总 alpha 质量不变）。

## 变换合并规则

只有与滤波语义严格一致的合并才允许（tryMerge）：

- copy + copy -> copy（整数平移复合仍是整数平移）；
- copy + F -> F、F + copy -> F：copy 是无损像素搬迁，复合仿射后采样结果
  逐位等价（测试断言合并管线与分级管线输出一致）；
- 其余组合（area+area、bilinear+bilinear、area+bilinear 等）一律返回
  null 拒绝——盒式滤波复合不等于一次盒式滤波，测试同时证明强行合并会产生
  可观测的错误结果（9->3->2 分级得 2，强融得 16/9）。

合并时同步复合裁剪域：每个 stage 携带 srcClip 凸多边形（默认整幅源图），
合并后新区域为两区域在源空间的精确交集（copy 侧平移、另一侧经 forward 映射），
保证 crop+affine 合并后与分级渲染在边界上逐像素一致。

## 脏区与增量渲染

- Stage.dirtyRect(srcDirty) 按滤波语义精确映射：copy/area 用正向映射后的
  平行四边形与目标像素方格求交（面积 > 0 才算），bilinear 用“采样中心落入
  脏区外扩 1px”的充要条件逐像素判定，结果裁剪到目标边界。
- Pipeline.renderDirty(input, dirty) 沿链传播脏区，只重渲染受影响区域；
  测试断言增量结果与全量重渲染逐像素一致、脏区外像素零改动、渲染行数远小于
  全量。
- Pipeline.updateStage(i, stage) 在变换变更时返回旧区域与新区域的并集
  （dirty = old ∪ new），并重渲染下游；测试验证旧位置被清空、新位置被绘制。

## 取消

渲染按行检查中止回调（或 CancelToken），触发即抛 CancelledError；测试
验证取消后下游 stage 一行都未执行、已渲染行数与中止点一致。

## 布局

- src/color.ts：sRGB <-> 线性转换
- src/affine.ts：2x3 仿射（乘法、求逆、奇异判定）
- src/geometry.ts：矩形运算与凸多边形裁剪/求交
- src/image.ts：RGBAImage（sRGB 字节）与 FloatImage（线性预乘浮点）
- src/stages.ts：Stage——crop / affine / scale / translate，三种滤波渲染与脏区映射
- src/pipeline.ts：Pipeline——全量/增量渲染、脏区传播、stage 更新、tryMerge/optimize
- test/：33 个测试；examples/demo.ts 运行样例（输出存于 examples/sample-output.txt）

## 测试结果

    ℹ tests 33
    ℹ pass 33
    ℹ fail 0

覆盖：裁剪与整数校验、任意仿射与旋转、奇异矩阵拒绝、非整倍比（7->3、9->3->2）
面积权重、1 像素图（1x1->3x3、1x1->1x1、5x5->1x1）、越界采样透明黑、透明红/
不透明蓝无红晕、线性空间 188 中点、旋转+缩小覆盖与单像素质量守恒、合并等价
与拒绝、局部编辑精确脏区、增量==全量、旧+新区域更新、逐行取消、
1x1 管线端到端。
