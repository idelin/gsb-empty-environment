# image-pyramid

增量式图像金字塔库（TypeScript，单通道 `Float64` 采样）。

- 每个父块由子层 2×2 邻域（四个子块）经滤波核降采样生成，滤波核可跨块取样。
- 局部编辑只失效并重建受影响的块，失效范围按像素精确计算。
- 金字塔边界采用镜像延拓（reflect-101：`-1 → 1`，`n → n-2`）。
- 基于纪元（epoch）的过期写入保护：在计算期间到达的新编辑会使在途结果被丢弃，旧结果绝不会覆盖更新的父块。

## 用法

```ts
import { ImagePyramid } from './src/pyramid.ts';

const pyr = new ImagePyramid({ width: 512, height: 512, blockSize: 16 });
pyr.edit(0, 0, 512, 512, pixels);   // 初始化底层
pyr.update();                        // 全量构建

pyr.edit(100, 100, 32, 32, patch);  // 局部编辑：仅标记受影响块
pyr.update(8);                       // 预算式增量更新（本轮最多 8 块，可未完成）
pyr.update();                        // 继续直至收敛，返回 true 表示全部干净

const level2 = pyr.getLevelPixels(2);
```

## 设计要点

**精确失效范围。** 父像素 `px` 依赖子像素 `[2px - r, 2px + r]`（`r` 为核半径）。
子层脏区间 `[c0, c1)` 映射到父层像素区间
`ceil((c0 - r) / 2) .. floor((c1 - 1 + r) / 2)`，逐层向上传播并夹取到层尺寸，
再按块对齐标记脏块。`pendingDirtyRect(level)` 可查询像素级精确失效范围。

**更新顺序。** `update()` 总是先处理最底层（最靠近底层）的脏块，保证任何父块
被重建时其依赖已全部干净；同一层内的块相互独立。`update(maxBlocks)` 支持
预算式分片，可在两轮未完成更新之间插入新编辑。

**过期提交防护。** 每次编辑使全局 epoch 递增，失效块被打上该版本。块在计算
开始时记录快照，提交前校验：块自身版本、以及其核足迹覆盖的所有子块版本
均不得超过快照，否则丢弃结果（`stats.rejectedCommits` 计数）并保持脏状态，
调度器会先重建依赖再重算该块。这保证了连续两轮未完成更新中，旧结果不会
覆盖新版本父块。

## 运行测试

需要 Node ≥ 22.6（类型擦除）或用 `tsc` 编译后运行：

```sh
node --experimental-strip-types --test "test/**/*.test.ts"
```
